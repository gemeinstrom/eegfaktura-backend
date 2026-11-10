package mailtemplate

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// ValidationError is a rejected input field; the API answers it with 400.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

const mailfileScheme = "mailfile"

// bmPolicy is the first sanitising pass, built from mail-html-policy.json.
var bmPolicy = policy.bluemondayPolicy()

// FileCheck reports whether an image id belongs to this community and mail.
type FileCheck func(id string) bool

// SanitizeBody returns the stored form of an EEG-provided mail text: allowed
// HTML only (mail-html-policy.json), links with rel, images only from the
// files of this mail, placeholders normalised to their label. An unknown
// placeholder or a text that is empty after cleaning is a ValidationError.
func SanitizeBody(def *Definition, in string, fileOK FileCheck) (string, error) {
	if len(in) > MaxBodyBytes {
		return "", &ValidationError{Field: "bodyHtml", Message: fmt.Sprintf("Der Text ist zu groß (höchstens %d KB).", MaxBodyBytes/1024)}
	}
	nodes, err := parseFragment(bmPolicy.Sanitize(in))
	if err != nil {
		return "", &ValidationError{Field: "bodyHtml", Message: "Der Text ist kein gültiges HTML."}
	}
	root := wrap(nodes)
	s := &sanitizer{def: def, fileOK: fileOK, field: "bodyHtml", root: root}
	if err := s.clean(root); err != nil {
		return "", err
	}
	if !hasContent(root) {
		return "", &ValidationError{Field: "bodyHtml", Message: "Der Text ist leer."}
	}
	breakTemplateBraces(root)
	out, err := renderChildren(root)
	if err != nil {
		return "", err
	}
	if len(out) > MaxBodyBytes {
		return "", &ValidationError{Field: "bodyHtml", Message: fmt.Sprintf("Der Text ist zu groß (höchstens %d KB).", MaxBodyBytes/1024)}
	}
	return out, nil
}

// subjectToken is a placeholder in a subject: {key} with the key syntax of the
// catalog (section H); anything else in braces, e.g. {EEG}, is text.
var subjectToken = regexp.MustCompile(`\{([a-z][A-Za-z]*(?:\.[A-Za-z]+)*)\}`)

var spaces = regexp.MustCompile(`\s+`)

// SanitizeSubject returns the stored form of a subject: plain text with
// placeholders as {key} tokens (inline placeholders of the mail only). Control
// characters and line breaks become spaces; a lone { or } stays text.
func SanitizeSubject(def *Definition, in string) (string, error) {
	subject := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, in)
	subject = strings.TrimSpace(spaces.ReplaceAllString(subject, " "))
	if subject == "" {
		return "", &ValidationError{Field: "subject", Message: "Der Betreff ist leer."}
	}
	if utf8.RuneCountInString(subject) > MaxSubjectChars {
		return "", &ValidationError{Field: "subject", Message: fmt.Sprintf("Der Betreff ist zu lang (höchstens %d Zeichen).", MaxSubjectChars)}
	}
	for _, m := range subjectToken.FindAllStringSubmatch(subject, -1) {
		ph := def.Placeholder(m[1])
		if ph == nil {
			return "", &ValidationError{Field: "subject", Message: "Unbekannter Platzhalter: " + m[1]}
		}
		if ph.Block {
			return "", &ValidationError{Field: "subject", Message: "Der Platzhalter " + m[1] + " ist im Betreff nicht möglich."}
		}
	}
	return subject, nil
}

type sanitizer struct {
	def    *Definition
	fileOK FileCheck
	field  string
	root   *html.Node
}

// clean applies the per-element rules of the policy to the children of n.
func (s *sanitizer) clean(n *html.Node) error {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch c.Type {
		case html.ElementNode:
			action, err := s.cleanElement(c)
			if err != nil {
				return err
			}
			if action == drop {
				n.RemoveChild(c)
				c = next
				continue
			}
			if action == unwrap {
				// unwrap: the children take the element's place and are cleaned in turn
				first := c.FirstChild
				for ch := c.FirstChild; ch != nil; {
					nx := ch.NextSibling
					c.RemoveChild(ch)
					n.InsertBefore(ch, c)
					ch = nx
				}
				n.RemoveChild(c)
				if first != nil {
					next = first
				}
				c = next
				continue
			}
			if c.FirstChild != nil && attr(c, "data-ph") == "" {
				if err := s.clean(c); err != nil {
					return err
				}
			}
		case html.TextNode:
		default:
			n.RemoveChild(c)
		}
		c = next
	}
	return nil
}

type elementAction int

const (
	keep elementAction = iota
	unwrap
	drop
)

// cleanElement filters the attributes of el and says what happens to it.
func (s *sanitizer) cleanElement(el *html.Node) (elementAction, error) {
	name := el.Data
	if _, ok := policy.Elements[name]; !ok {
		return unwrap, nil
	}
	var attrs []html.Attribute
	for _, a := range el.Attr {
		if a.Namespace != "" || !policy.allowsAttr(name, a.Key) {
			continue
		}
		switch {
		case a.Key == "style":
			a.Val = policy.cleanStyle(a.Val)
			if a.Val == "" {
				continue
			}
		case policy.urlSchemes(name, a.Key) != nil:
			clean, ok := s.cleanURL(name, a.Key, a.Val)
			if !ok {
				continue
			}
			a.Val = clean
		case name == "img" && a.Key == "width":
			a.Val = strings.TrimSpace(a.Val)
			if !imgWidth.MatchString(a.Val) {
				continue
			}
		case a.Key == "data-ph" && strings.TrimSpace(a.Val) == "":
			// an empty placeholder attribute counts as none: the element is unwrapped
			continue
		case a.Key == "data-ph":
			ph := s.def.Placeholder(a.Val)
			if ph == nil {
				return keep, &ValidationError{Field: s.field, Message: "Unbekannter Platzhalter: " + a.Val}
			}
			// a block (several lines) stands on its own, never inside a paragraph, list or placeholder
			if ph.Block && el.Parent != s.root {
				return keep, &ValidationError{Field: s.field, Message: "Der Baustein „" + ph.Label + "“ muss als eigener Absatz stehen, nicht in einem Absatz, einer Liste oder einem Zitat."}
			}
			// blocks are <div data-ph>, single values <span data-ph>
			if ph.Block {
				el.Data, el.DataAtom = "div", atom.Div
			} else {
				el.Data, el.DataAtom = "span", atom.Span
			}
			// the content of a placeholder is its label, never EEG text
			for el.FirstChild != nil {
				el.RemoveChild(el.FirstChild)
			}
			el.AppendChild(&html.Node{Type: html.TextNode, Data: ph.Label})
		}
		attrs = append(attrs, a)
	}
	el.Attr = attrs
	for _, req := range policy.requiredAttrs(name) {
		if attr(el, req) == "" {
			if el.FirstChild == nil {
				// e.g. an image without allowed source: nothing to keep
				return drop, nil
			}
			return unwrap, nil
		}
	}
	if name == "a" && policy.LinkRel != "" {
		el.Attr = append(el.Attr, html.Attribute{Key: "rel", Val: policy.LinkRel})
	}
	return keep, nil
}

// cleanURL returns the value to keep for a URL attribute, false to drop it:
// scheme of the policy for this attribute, a host for https, an address for
// mailto, and for mailfile: an image of this mail (ids compared in lower case).
func (s *sanitizer) cleanURL(el, key, val string) (string, bool) {
	val = trimSpaces(val)
	if strings.ContainsAny(val, " \t\r\n") {
		return "", false
	}
	u, err := url.Parse(val)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	allowed := false
	for _, sc := range policy.urlSchemes(el, key) {
		if sc == scheme {
			allowed = true
		}
	}
	if !allowed {
		return "", false
	}
	switch scheme {
	case mailfileScheme:
		id := strings.ToLower(u.Opaque)
		if id == BuiltinLogoId || (id != "" && s.fileOK != nil && s.fileOK(id)) {
			return mailfileScheme + ":" + id, true
		}
		return "", false
	case "https":
		// no % in the authority (bit%2Ely), as in eegfaktura-billing; url.Parse refuses a \ there already
		if u.Host == "" || u.Opaque != "" {
			return "", false
		}
		return val, !strings.Contains(upToPathEnd(val[strings.Index(val, "://")+3:]), "%")
	case "mailto":
		return val, strings.TrimSpace(u.Opaque) != ""
	}
	return val, true
}

// trimSpaces trims Unicode spaces (also NBSP) and ASCII whitespace from both
// ends of a URL, as eegfaktura-billing does.
func trimSpaces(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return unicode.Is(unicode.Z, r) || strings.ContainsRune(" \t\n\v\f\r", r)
	})
}

// MailfileId returns the file id (lower case) of an <img src="mailfile:..."> value.
func MailfileId(src string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(src))
	if err != nil || strings.ToLower(u.Scheme) != mailfileScheme {
		return "", false
	}
	id := strings.ToLower(u.Opaque)
	return id, id != ""
}

// ReferencedImages returns the file ids a stored body references.
func ReferencedImages(body string) []string {
	nodes, err := parseFragment(body)
	if err != nil {
		return nil
	}
	var ids []string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.DataAtom == atom.Img {
				if id, ok := MailfileId(attr(c, "src")); ok && id != BuiltinLogoId {
					ids = append(ids, id)
				}
			}
			walk(c)
		}
	}
	walk(wrap(nodes))
	return ids
}

func hasContent(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if strings.TrimSpace(strings.ReplaceAll(c.Data, " ", " ")) != "" {
				return true
			}
		case html.ElementNode:
			if c.DataAtom == atom.Img || attr(c, "data-ph") != "" || hasContent(c) {
				return true
			}
		}
	}
	return false
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key && a.Namespace == "" {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key && a.Namespace == "" {
			return true
		}
	}
	return false
}

func parseFragment(s string) ([]*html.Node, error) {
	return html.ParseFragment(strings.NewReader(s), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
}

// wrap puts parsed fragment nodes under one root, so they can be edited as a tree.
func wrap(nodes []*html.Node) *html.Node {
	root := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	return root
}

func renderChildren(root *html.Node) (string, error) {
	var buf bytes.Buffer
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&buf, c); err != nil {
			return "", err
		}
	}
	return buf.String(), nil
}

// breakTemplateBraces splits every "{{" in text as "{<!-- -->{", so no
// client-side template engine (e.g. in a webmail) evaluates EEG text or
// member values. Attributes are not touched (a "{{" in a link stays). Same
// rule as in eegfaktura-billing.
func breakTemplateBraces(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch c.Type {
		case html.TextNode:
			if i := strings.Index(c.Data, "{{"); i >= 0 {
				rest := &html.Node{Type: html.TextNode, Data: c.Data[i+1:]}
				c.Data = c.Data[:i+1]
				n.InsertBefore(&html.Node{Type: html.CommentNode, Data: " "}, next)
				n.InsertBefore(rest, next)
				next = rest // the rest may contain more
			}
		case html.ElementNode:
			breakTemplateBraces(c)
		}
		c = next
	}
}
