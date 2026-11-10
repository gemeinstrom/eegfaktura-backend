package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"

	"at.ourproject/vfeeg-backend/config"
	"at.ourproject/vfeeg-backend/mailtemplate"
	"at.ourproject/vfeeg-backend/model"
	"github.com/gabriel-vasile/mimetype"
	"github.com/pborman/uuid"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Finding is something the operator has to know about a conversion. A blocking
// finding stops the import of that mail: it is never written half-converted.
type Finding struct {
	Blocking bool
	Text     string
}

// Upload is a file the import uploads before saving the text. PreId is the id
// the converted body uses until the API returns the real one.
type Upload struct {
	PreId    string
	Kind     string
	Filename string
	MimeType string
	Content  []byte
}

// Conversion is one hand-made mail template converted to the new format.
type Conversion struct {
	Tenant   string
	Key      string
	Config   string
	Subject  string
	Body     string
	Files    []Upload
	Findings []Finding
}

// AttachmentPreId returns the pre-upload id of the PDF, "" for none.
func (c *Conversion) AttachmentPreId() string {
	for _, f := range c.Files {
		if f.Kind == "attachment" {
			return f.PreId
		}
	}
	return ""
}

// Blocked reports whether the mail must not be imported.
func (c *Conversion) Blocked() bool {
	for _, f := range c.Findings {
		if f.Blocking {
			return true
		}
	}
	return false
}

func (c *Conversion) block(format string, args ...interface{}) {
	c.Findings = append(c.Findings, Finding{Blocking: true, Text: fmt.Sprintf(format, args...)})
}

func (c *Conversion) note(format string, args ...interface{}) {
	c.Findings = append(c.Findings, Finding{Text: fmt.Sprintf(format, args...)})
}

// ConvertTenant converts every legacy mail config found in a tenant's templates
// directory (volume layout <base>/<TENANT>/templates).
func ConvertTenant(fsys fs.FS, tenant string) []*Conversion {
	var out []*Conversion
	for _, def := range mailtemplate.Catalog() {
		if _, err := fs.Stat(fsys, def.LegacyConfig); err != nil {
			continue
		}
		def := def
		out = append(out, convert(fsys, tenant, &def))
	}
	return out
}

func convert(fsys fs.FS, tenant string, def *mailtemplate.Definition) *Conversion {
	c := &Conversion{Tenant: tenant, Key: def.Key, Config: def.LegacyConfig, Subject: def.DefaultSubject}
	cfg, err := config.ReadActivationMailTemplateConfig(fsys, def.LegacyConfig)
	if err != nil {
		c.block("%v", err)
		return c
	}
	raw, err := fs.ReadFile(fsys, cfg.TemplateFile)
	if err != nil {
		c.block("Vorlage %q fehlt", cfg.TemplateFile)
		return c
	}

	c.checkActionsInAttributes(string(raw))
	if c.Blocked() {
		return c
	}
	text := c.convertActions(def, string(raw))
	if c.Blocked() {
		return c
	}

	doc, err := html.Parse(strings.NewReader(text))
	if err != nil {
		c.block("Vorlage ist kein HTML: %v", err)
		return c
	}
	body := findBody(doc)
	if body == nil {
		c.block("Vorlage hat keinen <body>")
		return c
	}
	c.dropLegacySignature(body)
	preIds := c.convertImages(fsys, def, cfg.InlinePictures, body)
	keepDivLines(body)
	c.noteFormattingLosses(body)
	c.convertAttachment(fsys, def, cfg.Attachment.Name)
	if c.Blocked() {
		return c
	}

	var buf bytes.Buffer
	for n := body.FirstChild; n != nil; n = n.NextSibling {
		_ = html.Render(&buf, n)
	}
	clean, err := mailtemplate.SanitizeBody(def, buf.String(), func(id string) bool { return preIds[id] })
	if err != nil {
		c.block("Bereinigung: %v", err)
		return c
	}
	// the save would refuse them: the community has to shorten the links first
	for _, p := range mailtemplate.CheckLinks(clean) {
		c.block("Links: %s", p.Message)
	}
	c.Body = clean
	return c
}

// dropLegacySignature removes the closing "versandt durch" paragraph and the
// eegfaktura logo after it from the end of a legacy template: the server now
// ends every member mail with the fixed footer, which says the same. A logo
// elsewhere in the text stays (as the built-in image).
func (c *Conversion) dropLegacySignature(body *html.Node) {
	logo := lastElement(body)
	if logo == nil {
		return
	}
	img := logo
	if logo.DataAtom == atom.P && strings.TrimSpace(textOf(logo)) == "" {
		img = lastElement(logo)
	}
	if img == nil || img.DataAtom != atom.Img || attrValue(img, "src") != "cid:eegfaktura-logo-1" {
		return
	}
	prev := logo.PrevSibling
	for prev != nil && prev.Type != html.ElementNode {
		if prev.Type == html.TextNode && strings.TrimSpace(prev.Data) != "" {
			return
		}
		prev = prev.PrevSibling
	}
	if prev == nil || prev.DataAtom != atom.P || !strings.EqualFold(strings.TrimSpace(textOf(prev)), "versandt durch") {
		return
	}
	body.RemoveChild(logo)
	body.RemoveChild(prev)
	c.note("Abschluss „versandt durch“ mit eegfaktura-Logo entfällt; jede Mail endet jetzt mit der festen Fußzeile „%s“", mailtemplate.FixedFooterText)
}

// lastElement returns the last child element of n, nil if text that is not
// only whitespace or no element follows.
func lastElement(n *html.Node) *html.Node {
	for ch := n.LastChild; ch != nil; ch = ch.PrevSibling {
		switch {
		case ch.Type == html.ElementNode:
			return ch
		case ch.Type == html.TextNode && strings.TrimSpace(ch.Data) != "":
			return nil
		}
	}
	return nil
}

// textOf returns the text content of n.
func textOf(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type == html.TextNode {
				b.WriteString(ch.Data)
			}
			rec(ch)
		}
	}
	rec(n)
	return b.String()
}

// convertImages rewrites cid: images to mailfile: ids and queues their upload.
// It returns the ids the sanitiser may keep.
func (c *Conversion) convertImages(fsys fs.FS, def *mailtemplate.Definition, pictures []model.InlinePicture, body *html.Node) map[string]bool {
	byCid := map[string]model.InlinePicture{}
	for _, p := range pictures {
		byCid[p.ContentId] = p
	}
	preIds := map[string]bool{}
	assigned := map[string]string{}
	used := map[string]bool{}
	walk(body, func(n *html.Node) {
		if n.DataAtom != atom.Img {
			return
		}
		src := attrValue(n, "src")
		cid, ok := strings.CutPrefix(src, "cid:")
		if !ok {
			c.note("Bild %q ist keine eingebettete Datei und entfällt", src)
			return
		}
		p, ok := byCid[cid]
		if !ok {
			c.note("Bild cid:%s hat keine Datei in der Konfiguration und entfällt", cid)
			return
		}
		used[cid] = true
		if cid == "eegfaktura-logo-1" || path.Base(p.Filepath) == "eegfaktura-logo.png" {
			setAttribute(n, "src", "mailfile:"+mailtemplate.BuiltinLogoId)
			setWidth(n, 335)
			return
		}
		id, ok := assigned[cid]
		if !ok {
			content, err := fs.ReadFile(fsys, p.Filepath)
			if err != nil {
				c.block("Bild %q fehlt", p.Filepath)
				return
			}
			mt := mimetype.Detect(content)
			if !mt.Is("image/png") && !mt.Is("image/jpeg") {
				c.block("Bild %q ist %s, erlaubt sind PNG und JPEG", p.Filepath, mt.String())
				return
			}
			if int64(len(content)) > def.Limits.MaxImageBytes {
				c.block("Bild %q ist %d KB groß, höchstens %d KB erlaubt — vorher verkleinern", p.Filepath, len(content)/1024, def.Limits.MaxImageBytes/1024)
				return
			}
			id = uuid.New()
			assigned[cid] = id
			preIds[id] = true
			mimeType := "image/png"
			if mt.Is("image/jpeg") {
				mimeType = "image/jpeg"
			}
			c.Files = append(c.Files, Upload{PreId: id, Kind: "image", Filename: path.Base(p.Filepath), MimeType: mimeType, Content: content})
		}
		setAttribute(n, "src", "mailfile:"+id)
		if w := displayWidth(fsys, p.Filepath, attrValue(n, "style"), attrValue(n, "width")); w > 0 {
			setWidth(n, w)
		}
	})

	for _, p := range pictures {
		if !used[p.ContentId] {
			c.note("Bild %q (cid:%s) wird im Text nicht verwendet und nicht übernommen", p.Filepath, p.ContentId)
		}
	}
	if len(assigned) > def.Limits.MaxImages {
		c.block("%d Bilder, höchstens %d erlaubt", len(assigned), def.Limits.MaxImages)
	}
	c.checkTotal(def)
	return preIds
}

func (c *Conversion) convertAttachment(fsys fs.FS, def *mailtemplate.Definition, name string) {
	if name == "" {
		return
	}
	if def.Limits.MaxAttachments == 0 {
		c.block("Anhang %q: PDF-Anhänge sind derzeit nicht möglich", name)
		return
	}
	content, err := fs.ReadFile(fsys, name)
	if err != nil {
		c.block("Anhang %q fehlt", name)
		return
	}
	if !mimetype.Detect(content).Is("application/pdf") {
		c.block("Anhang %q ist kein PDF", name)
		return
	}
	if int64(len(content)) > def.Limits.MaxAttachmentBytes {
		c.block("Anhang %q ist %d KB groß, höchstens %d KB erlaubt", name, len(content)/1024, def.Limits.MaxAttachmentBytes/1024)
		return
	}
	c.Files = append(c.Files, Upload{PreId: uuid.New(), Kind: "attachment", Filename: path.Base(name), MimeType: "application/pdf", Content: content})
	c.checkTotal(def)
}

func (c *Conversion) checkTotal(def *mailtemplate.Definition) {
	var total int64
	for _, f := range c.Files {
		total += int64(len(f.Content))
	}
	if total > def.Limits.MaxTotalBytes {
		c.block("Dateien zusammen %d KB, höchstens %d KB erlaubt", total/1024, def.Limits.MaxTotalBytes/1024)
	}
}

// noteFormattingLosses lists what the sanitiser will remove, so the community
// can be told before the switch (fonts, colours, tables, …).
func (c *Conversion) noteFormattingLosses(body *html.Node) {
	allowed := map[string]bool{}
	for _, el := range mailtemplate.AllowedElements() {
		allowed[el] = true
	}
	elements := map[string]bool{}
	styles := map[string]bool{}
	links := map[string]bool{}
	walk(body, func(n *html.Node) {
		if !allowed[n.Data] && n.Data != "body" {
			elements[n.Data] = true
		}
		for _, decl := range strings.Split(attrValue(n, "style"), ";") {
			if prop, _, ok := strings.Cut(decl, ":"); ok {
				if p := strings.ToLower(strings.TrimSpace(prop)); p != "text-align" && p != "" {
					styles[p] = true
				}
			}
		}
		if n.DataAtom == atom.A {
			href := strings.ToLower(strings.TrimSpace(attrValue(n, "href")))
			if href != "" && !strings.HasPrefix(href, "https:") && !strings.HasPrefix(href, "mailto:") {
				links[attrValue(n, "href")] = true
			}
		}
	})
	if len(elements) > 0 {
		c.note("Formatierung entfällt, Elemente: %s (Inhalt bleibt, außer bei style/script)", strings.Join(keys(elements), ", "))
	}
	if len(styles) > 0 {
		c.note("Formatierung entfällt, Stilangaben: %s", strings.Join(keys(styles), ", "))
	}
	if len(links) > 0 {
		c.note("Links ohne https:/mailto: verlieren den Link (Text bleibt): %s", strings.Join(keys(links), " "))
	}
}

var blockElements = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Ul: true, atom.Ol: true, atom.Li: true, atom.Table: true, atom.Tr: true,
	atom.Td: true, atom.Blockquote: true, atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true,
	atom.H6: true, atom.Br: false,
}

// keepDivLines keeps the line breaks of hand-made templates: the policy unwraps
// a <div> without placeholder, so a div that holds only inline content (a
// signature line) becomes its content followed by <br>.
func keepDivLines(body *html.Node) {
	var leaves []*html.Node
	walk(body, func(n *html.Node) {
		if n.DataAtom != atom.Div || attrValue(n, "data-ph") != "" {
			return
		}
		leaf := true
		walk(n, func(d *html.Node) {
			if blockElements[d.DataAtom] {
				leaf = false
			}
		})
		if leaf {
			leaves = append(leaves, n)
		}
	})
	for _, n := range leaves {
		parent := n.Parent
		for ch := n.FirstChild; ch != nil; {
			next := ch.NextSibling
			n.RemoveChild(ch)
			parent.InsertBefore(ch, n)
			ch = next
		}
		parent.InsertBefore(&html.Node{Type: html.ElementNode, Data: "br", DataAtom: atom.Br}, n)
		parent.RemoveChild(n)
	}
}

// displayWidth turns the size limits of a legacy <img style> into a width
// attribute, the only size the policy allows, so screenshots keep their size.
func displayWidth(fsys fs.FS, file, style, width string) int {
	if w, err := strconv.Atoi(strings.TrimSuffix(width, "px")); err == nil && w > 0 {
		return w
	}
	f, err := fsys.Open(file)
	if err != nil {
		return 0
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return 0
	}
	w := float64(cfg.Width)
	for _, m := range styleLength.FindAllStringSubmatch(style, -1) {
		v, _ := strconv.ParseFloat(m[2], 64)
		switch strings.ToLower(m[1]) {
		case "max-height":
			w = math.Min(w, float64(cfg.Width)*v/float64(cfg.Height))
		case "height":
			w = float64(cfg.Width) * v / float64(cfg.Height)
		case "max-width":
			w = math.Min(w, v)
		case "width":
			w = v
		}
	}
	return int(math.Round(w))
}

func findBody(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == atom.Body {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if b := findBody(c); b != nil {
			return b
		}
	}
	return nil
}

func walk(n *html.Node, f func(*html.Node)) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			f(c)
		}
		walk(c, f)
	}
}

func attrValue(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func setAttribute(n *html.Node, key, val string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func setWidth(n *html.Node, w int) {
	setAttribute(n, "width", strconv.Itoa(w))
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func unique(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
