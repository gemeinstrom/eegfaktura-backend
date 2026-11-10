package logging

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The never-log test (concept W-261008-ca2, point 4; dev#45 L11). It
// type-checks the module's source and fails on
//   - a struct (a model above all) printed whole: by a format verb, as a bare
//     argument or as a field value. Errors and Stringers print what they choose.
//   - a name of personal data or a secret among the identifiers in the
//     arguments of a log call (variables, fields, methods). Identifiers are
//     split into words (camelCase, snake_case) and compared as whole words
//     with neverLogWords, also with a plural s: "subscription" is not "sub",
//     "tokens" is "token". In an all lower-case identifier a list word of five
//     or more letters also matches inside it (accesstoken).
//   - a list word right before a verb of a format text ("password=%s").
//     Values that are numbers, booleans or errors are skipped by type.
//     Otherwise string literals are not scanned; a field key outside
//     allowedFields is dropped at runtime.
//
// A log call is logrus, log/slog, log or fmt.Print*. Generated code is left
// out. A line that is fine goes on neverLogAllowed with its reason, after
// reading what the value holds at runtime. A false positive is fixed in the
// scanner, not allowlisted.

// neverLogWords is the list common to all services. An entry of several words
// (user name, api_key) matches those words in a row: userName, api_key, APIKey.
// Metering point numbers are ids and may be logged (W-261008-ca2).
var neverLogWords = wordSet("password", "passwd", "pwd", "secret", "token", "jwt", "bearer", "authorization",
	"credential", "creds", "cookie", "apikey", "iban", "bic", "firstname", "lastname", "vorname", "nachname",
	"name1", "name2", "street", "phone", "email", "mailaddress", "recipient", "sentto", "owner", "accountholder",
	"username", "userid", "sub", "subject", "payload", "body")

// neverLogAllowed: key "<file>: <finding>", without a line number, so an entry
// survives edits around it but not a change of what is logged.
var neverLogAllowed = map[string]string{
	"eda/edaSubscription.go: %+v of []eda.responseCodesPerMeter": "metering point, response codes, consent end and a " +
		"pointer to the consent id: no personal data, metering point numbers may be logged",
	"cmd/mailtemplate-import/main.go: name Body in c.Body":       "operator tool, prints the template on its own terminal, not a service log",
	"cmd/mailtemplate-import/main.go: name Subject in c.Subject": "operator tool, prints the template on its own terminal, not a service log",
	"cmd/mailtemplate-import/main.go: name SentTo in sent.SentTo": "operator tool, prints the test mail address the " +
		"operator passed on its own terminal, not a service log",
	"eda/edaSubscription.go: name Payload in msg.Payload.ConversationId": "only the conversation id is logged; " +
		"the struct itself is caught as a whole value",
	"mqtt/subscriptionHandler.go: name Payload in msg.Payload.ConversationId": "only the conversation id is logged",
	"database/eegDao.go: name owners in owners":                               "the tenants (community ids) holding the metering points, no person",
	"database/eegDao.go: name owners in owners[0]":                            "the tenant (community id) the metering points are applied to, no person",
	"eda/edaSubscription.go: name Payload in msg.Payload.EcId": "the community id of a dropped inbound EDA message, " +
		"no person (eegfaktura-dev#56 #12, 261105-mm5)",
}

// fixtureDir is left out of TestNeverLog; TestNeverLog_FindsTheFixture checks it.
const fixtureDir = "logging/testdata/"

// The scan compiles the module through go list. -short skips it for a quick
// local run; CI runs the tests without -short, so it always runs there.
func TestNeverLog(t *testing.T) {
	if testing.Short() {
		t.Skip("source scan, skipped with -short")
	}
	hits, err := scanned()
	require.NoError(t, err)
	var failed []string
	used := map[string]bool{}
	for _, h := range hits {
		if strings.HasPrefix(h.file, fixtureDir) {
			continue
		}
		if _, ok := neverLogAllowed[h.key]; ok {
			used[h.key] = true
			continue
		}
		failed = append(failed, fmt.Sprintf("%s:%d: %s", h.file, h.line, h.what))
	}
	for k := range neverLogAllowed {
		if !used[k] {
			failed = append(failed, "stale neverLogAllowed entry: "+k)
		}
	}
	assert.Empty(t, failed, "never-log rule, see the head of this file:\n%s", strings.Join(failed, "\n"))
}

// The scan itself: every line marked "// want" in the fixture is a hit and no other is.
func TestNeverLog_FindsTheFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("source scan, skipped with -short")
	}
	const fixture = fixtureDir + "neverlog/bad.go"
	src, err := os.ReadFile(filepath.FromSlash("testdata/neverlog/bad.go"))
	require.NoError(t, err)
	want := map[int]bool{}
	for i, l := range strings.Split(string(src), "\n") {
		if strings.HasSuffix(strings.TrimSpace(l), "// want") {
			want[i+1] = true
		}
	}
	require.NotEmpty(t, want)

	hits, err := scanned()
	require.NoError(t, err)
	got := map[int]bool{}
	for _, h := range hits {
		if strings.HasPrefix(h.file, fixtureDir) {
			require.Equal(t, fixture, h.file)
			got[h.line] = true
		}
	}
	assert.Equal(t, want, got)
}

func TestIdentWords(t *testing.T) {
	for id, want := range map[string][]string{
		"userName": {"user", "name"}, "APIKey": {"api", "key"}, "api_key": {"api", "key"}, "Name1": {"name1"},
		"IBAN": {"iban"}, "userID": {"user", "id"}, "ConversationId": {"conversation", "id"}, "_x": {"x"},
		"IBANs": {"ibans"}, "IBANsFor": {"ibans", "for"}, "URLStatus": {"url", "status"},
	} {
		assert.Equal(t, want, identWords(id), id)
	}
	for id, want := range map[string]bool{
		"userName": true, "APIKey": true, "apikey": true, "eMail": true, "sentTo": true, "tokenString": true,
		"Subscription": false, "subscription": false, "tokenizerState": false, "Number": false, "subject": true, "Sub": true,
		"tokens": true, "IBANs": true, "Emails": true, "recipients": true, "accesstoken": true, "userpassword": true,
		"mailaddress": true, "tokenizer": true, "subid": false, "mypwd": false, "Accesstoken": false,
	} {
		assert.Equal(t, want, hasNeverLogWord(id), id)
	}
}

type neverLogHit struct {
	key, file, what string
	line            int
}

type listedPackage struct {
	ImportPath, Dir, Export string
	GoFiles                 []string
	Module                  *struct{ Main bool }
}

// scanned runs go list and the scan once per test binary, also under -count=N:
// the module and the fixture in one go.
var scanned = sync.OnceValues(func() ([]neverLogHit, error) {
	return scanPackages("./...", "./"+fixtureDir+"neverlog")
})

func scanPackages(patterns ...string) ([]neverLogHit, error) {
	root, err := filepath.Abs("..")
	if err != nil {
		return nil, err
	}
	args := append([]string{"list", "-export", "-deps", "-json=ImportPath,Dir,Export,GoFiles,Module"}, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	var failed *exec.ExitError
	if errors.As(err, &failed) {
		return nil, fmt.Errorf("go list: %w: %s", err, failed.Stderr)
	} else if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}

	exports := map[string]string{}
	var own []listedPackage
	for dec := json.NewDecoder(strings.NewReader(string(out))); ; {
		var p listedPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		exports[p.ImportPath] = p.Export
		if p.Module != nil && p.Module.Main {
			own = append(own, p)
		}
	}

	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) { return os.Open(exports[path]) })
	s := &scanner{fset: fset, root: root}
	for _, p := range own {
		var files []*ast.File
		for _, name := range p.GoFiles {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.ParseComments)
			if err != nil {
				return nil, err
			}
			files = append(files, f)
		}
		s.info = &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}}
		if _, err := (&types.Config{Importer: imp}).Check(p.ImportPath, fset, files, s.info); err != nil {
			return nil, fmt.Errorf("%s: %w", p.ImportPath, err)
		}
		for _, f := range files {
			if !ast.IsGenerated(f) {
				ast.Inspect(f, s.visit)
			}
		}
	}
	return s.hits, nil
}

type scanner struct {
	fset *token.FileSet
	info *types.Info
	root string
	hits []neverLogHit
}

func (s *scanner) hit(pos token.Pos, what string) {
	p := s.fset.Position(pos)
	file, _ := filepath.Rel(s.root, p.Filename)
	file = filepath.ToSlash(file)
	s.hits = append(s.hits, neverLogHit{key: file + ": " + what, file: file, what: what, line: p.Line})
}

func (s *scanner) visit(n ast.Node) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return true
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return true
	}
	name := sel.Sel.Name
	switch s.packageOf(sel) {
	case "github.com/sirupsen/logrus", "log":
		switch {
		case name == "WithField" || name == "WithFields":
			s.fields(call.Args)
		case name == "Logf":
			s.formatted(call.Args[1:])
		case logMethods[strings.TrimSuffix(strings.TrimSuffix(name, "ln"), "f")]:
			if strings.HasSuffix(name, "f") {
				s.formatted(call.Args)
			} else {
				s.plain(call.Args)
			}
		}
	case "log/slog":
		if logMethods[strings.TrimSuffix(name, "Context")] || name == "With" || name == "LogAttrs" {
			s.fields(call.Args)
		}
	case "fmt":
		switch name {
		case "Print", "Println":
			s.plain(call.Args)
		case "Printf":
			s.formatted(call.Args)
		}
	}
	return true
}

// packageOf is the package of a function, or of the type of a method's receiver.
func (s *scanner) packageOf(sel *ast.SelectorExpr) string {
	if id, ok := sel.X.(*ast.Ident); ok {
		if pn, ok := s.info.Uses[id].(*types.PkgName); ok {
			return pn.Imported().Path()
		}
	}
	t := s.info.Types[sel.X].Type
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok && n.Obj().Pkg() != nil {
		return n.Obj().Pkg().Path()
	}
	return ""
}

var logMethods = map[string]bool{
	"Print": true, "Trace": true, "Debug": true, "Info": true, "Warn": true, "Warning": true,
	"Error": true, "Fatal": true, "Panic": true, "Log": true,
}

var (
	formatVerb = regexp.MustCompile(`%([-+# 0]*)[0-9]*(?:\.[0-9]+)?([a-zA-Z%])`)
	// a list word right before a verb: "password=%s", "email: %v", "IBANs %v"
	beforeVerb = regexp.MustCompile(`(?i)\b(?:` + strings.Join(append(mapKeys(neverLogWords), "api_key", "mail address"), "|") +
		`)s?[ =:'"(-]{0,3}$`)
	errorType = types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
)

func mapKeys(m map[string]bool) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func (s *scanner) plain(args []ast.Expr) {
	for _, a := range args {
		s.names(a)
		s.whole(a, "bare argument")
	}
}

func (s *scanner) formatted(args []ast.Expr) {
	format, values := args[0], args[1:]
	for _, a := range values {
		s.names(a)
	}
	tv := s.info.Types[format]
	if tv.Value == nil || tv.Value.Kind() != constant.String {
		for _, a := range values {
			s.whole(a, "argument of a variable format")
		}
		return
	}
	text := constant.StringVal(tv.Value)
	i := 0
	for _, m := range formatVerb.FindAllStringSubmatchIndex(text, -1) {
		flags, verb := text[m[2]:m[3]], text[m[4]:m[5]]
		if verb == "%" || i >= len(values) {
			continue
		}
		if w := beforeVerb.FindString(text[:m[0]]); w != "" && carriesText(s.typeOf(values[i])) {
			s.hit(format.Pos(), fmt.Sprintf("word before a verb in the format text %q", w+text[m[0]:m[1]]))
		}
		if verb != "T" && verb != "p" {
			s.whole(values[i], "%"+flags+verb)
			s.dumpedInterface(values[i], flags)
		}
		i++
	}
}

// fields looks at the values of WithField(s) and slog calls.
func (s *scanner) fields(args []ast.Expr) {
	for _, a := range args {
		s.names(a)
		if lit, ok := a.(*ast.CompositeLit); ok {
			for _, e := range lit.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					s.whole(kv.Value, "field value")
				}
			}
		} else {
			s.whole(a, "field value")
		}
	}
}

// names reports identifiers (variables, fields, methods) in a logged argument
// that hold a word of neverLogWords. Left out is what the line does not show
// as text: a value that is a number, a boolean or an error (carriesText), and
// the argument of len or cap.
func (s *scanner) names(e ast.Expr) {
	ast.Inspect(e, func(n ast.Node) bool {
		if x, ok := n.(ast.Expr); ok && !carriesText(s.typeOf(x)) {
			return false
		}
		switch n := n.(type) {
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && (id.Name == "len" || id.Name == "cap") {
				if _, builtin := s.info.Uses[id].(*types.Builtin); builtin {
					return false
				}
			}
		case *ast.Ident:
			if _, pkg := s.info.Uses[n].(*types.PkgName); !pkg && hasNeverLogWord(n.Name) {
				s.hit(n.Pos(), "name "+n.Name+" in "+types.ExprString(e))
			}
		}
		return true
	})
}

// typeOf is the type of an expression, also of an identifier that the type
// checker records only in Uses; nil when unknown.
func (s *scanner) typeOf(e ast.Expr) types.Type {
	if tv, ok := s.info.Types[e]; ok {
		return tv.Type
	}
	if id, ok := e.(*ast.Ident); ok {
		if v, ok := s.info.Uses[id].(*types.Var); ok {
			return v.Type()
		}
	}
	return nil
}

// carriesText: whether a value can show a name or a secret. Numbers, booleans
// and error values cannot by their type (an error prints what it chooses).
func carriesText(t types.Type) bool {
	if t == nil {
		return true
	}
	if b, ok := t.Underlying().(*types.Basic); ok && b.Info()&(types.IsNumeric|types.IsBoolean) != 0 {
		return false
	}
	return !types.Implements(t, errorType)
}

func wordSet(ws ...string) map[string]bool {
	m := map[string]bool{}
	for _, w := range ws {
		m[w] = true
	}
	return m
}

// hasNeverLogWord: one word, or several in a row, of the identifier form an
// entry, also with a plural s. An all lower-case identifier also matches when
// it holds an entry of five or more letters (accesstoken, userpassword).
func hasNeverLogWord(id string) bool {
	ws := identWords(id)
	for i := range ws {
		joined := ""
		for _, w := range ws[i:] {
			joined += w
			if neverLogWords[joined] || neverLogWords[strings.TrimSuffix(joined, "s")] {
				return true
			}
		}
	}
	if strings.ToLower(id) == id {
		for w := range neverLogWords {
			if len(w) >= 5 && strings.Contains(id, w) {
				return true
			}
		}
	}
	return false
}

// identWords splits an identifier into lower-case words at underscores and
// case changes: userName -> user, name; APIKey -> api, key; Name1 -> name1;
// a plural s stays with an acronym: IBANs -> ibans.
func identWords(id string) []string {
	var ws []string
	rs := []rune(id)
	start := 0
	for i, r := range rs {
		switch {
		case r == '_':
			if i > start {
				ws = append(ws, strings.ToLower(string(rs[start:i])))
			}
			start = i + 1
		case i > start && unicode.IsUpper(r) && (!unicode.IsUpper(rs[i-1]) || startsWord(rs, i)):
			ws = append(ws, strings.ToLower(string(rs[start:i])))
			start = i
		}
	}
	if start < len(rs) {
		ws = append(ws, strings.ToLower(string(rs[start:])))
	}
	return ws
}

// startsWord: the upper-case letter at i of an acronym begins a new word
// (APIKey: K), unless only a plural s follows it (IBANs).
func startsWord(rs []rune, i int) bool {
	if i+1 >= len(rs) || !unicode.IsLower(rs[i+1]) {
		return false
	}
	return rs[i+1] != 's' || i+2 < len(rs) && unicode.IsLower(rs[i+2])
}

func (s *scanner) whole(e ast.Expr, how string) {
	if t := s.info.Types[e].Type; t != nil && printsWhole(t) {
		s.hit(e.Pos(), how+" of "+types.TypeString(t, func(p *types.Package) string { return p.Name() }))
	}
}

// dumpedInterface: %+v and %#v of an interface can hold anything, also a client
// with its options. A plain %v is left alone, it is too common for a bare value.
func (s *scanner) dumpedInterface(e ast.Expr, flags string) {
	t := s.info.Types[e].Type
	if t == nil || !strings.ContainsAny(flags, "+#") || hasPrintMethod(t) {
		return
	}
	if _, ok := t.Underlying().(*types.Interface); ok {
		s.hit(e.Pos(), "%"+flags+"v of "+types.TypeString(t, func(p *types.Package) string { return p.Name() }))
	}
}

func printsWhole(t types.Type) bool {
	if hasPrintMethod(t) {
		return false
	}
	if n, ok := t.(*types.Named); ok && n.Obj().Pkg() != nil &&
		n.Obj().Pkg().Path() == "net/http" && n.Obj().Name() == "Header" {
		return true // holds Authorization and cookies
	}
	switch u := t.Underlying().(type) {
	case *types.Struct:
		return true
	case *types.Pointer:
		return printsWhole(u.Elem())
	case *types.Slice:
		return printsWhole(u.Elem())
	case *types.Array:
		return printsWhole(u.Elem())
	case *types.Map:
		return printsWhole(u.Elem())
	}
	return false
}

// hasPrintMethod: an error or Stringer prints itself, not its fields.
func hasPrintMethod(t types.Type) bool {
	for _, name := range []string{"Error", "String"} {
		if obj, _, _ := types.LookupFieldOrMethod(t, true, nil, name); obj != nil {
			if f, ok := obj.(*types.Func); ok && f.Signature().Params().Len() == 0 {
				return true
			}
		}
	}
	return false
}
