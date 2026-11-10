package mailtemplate

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"strings"

	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/public"
	"at.ourproject/vfeeg-backend/services"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Data holds the values of the placeholders of one mail.
type Data struct {
	FirstName      string
	LastName       string
	Number         string
	MeteringPoints []string
	GridOperator   string
	Direction      string
	EndDate        string
	Reason         string
	Eeg            *model.Eeg
}

// ImageMode says how images are referenced in the rendered HTML.
type ImageMode int

const (
	// ImagesInline references images as cid: and returns them as inline attachments (sending).
	ImagesInline ImageMode = iota
	// ImagesDataURI embeds images as data: URIs (preview).
	ImagesDataURI
)

// Rendered is a mail ready to send.
type Rendered struct {
	Subject    string
	Html       string
	Inline     []*services.Attachment
	Attachment *services.Attachment
}

// NoAttachmentMessage is the answer to a PDF attachment while a mail allows none.
const NoAttachmentMessage = "PDF-Anhänge sind derzeit nicht möglich; bitte den Anhang entfernen."

// ErrTooLarge is returned when the files of a mail exceed its size limit.
var ErrTooLarge = errors.New("mail too large")

// ErrMissingFile is returned when a file the mail uses is not available.
var ErrMissingFile = errors.New("file of the mail not available")

// Render renders a stored (or edited) subject and body with the placeholder
// values in the fixed outer frame. The body is sanitised again first, so a
// stored text never reaches a member unchecked. files are the files of this
// mail of this community the caller allows (with content); attachmentId names
// the PDF, "" for none.
func Render(def *Definition, subject, body, attachmentId string, data *Data, files []model.MailTemplateFile, mode ImageMode) (*Rendered, error) {
	byId := map[string]*model.MailTemplateFile{}
	var attachment *model.MailTemplateFile
	for i := range files {
		f := &files[i]
		switch {
		case f.Kind == model.MailFileKindImage:
			byId[f.Id] = f
		case f.Kind == model.MailFileKindAttachment && f.Id == attachmentId:
			attachment = f
		}
	}
	// the limits apply to stored texts saved before them too
	if attachmentId != "" && def.Limits.MaxAttachments == 0 {
		return nil, &ValidationError{Field: "attachmentFileId", Message: NoAttachmentMessage}
	}
	if n := countDistinct(ReferencedImages(body)); n > def.Limits.MaxImages {
		return nil, &ValidationError{Field: "bodyHtml", Message: fmt.Sprintf("Höchstens %d Bilder je Mail (jetzt %d).", def.Limits.MaxImages, n)}
	}
	if attachmentId != "" && attachment == nil {
		return nil, fmt.Errorf("%w: PDF attachment %s", ErrMissingFile, attachmentId)
	}
	// an image the text uses but that is not available fails the mail instead of
	// silently disappearing (the sender then falls back to the default)
	for _, id := range ReferencedImages(body) {
		if byId[id] == nil {
			return nil, fmt.Errorf("%w: image %s", ErrMissingFile, id)
		}
	}

	cleanSubject, err := SanitizeSubject(def, subject)
	if err != nil {
		return nil, err
	}
	cleanBody, err := SanitizeBody(def, body, func(id string) bool { return byId[id] != nil })
	if err != nil {
		return nil, err
	}
	if errs := LinkErrors(cleanBody); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Message
		}
		return nil, &ValidationError{Field: "bodyHtml", Message: strings.Join(msgs, " ")}
	}

	renderedSubject, err := renderSubject(def, cleanSubject, data)
	if err != nil {
		return nil, err
	}

	nodes, err := parseFragment(cleanBody)
	if err != nil {
		return nil, err
	}
	root := wrap(nodes)
	out := &Rendered{Subject: renderedSubject}
	var fileBytes int64
	used := map[string]bool{}
	var walkErr error
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		for c := n.FirstChild; c != nil && walkErr == nil; {
			next := c.NextSibling
			if c.Type == xhtml.ElementNode {
				if key := attr(c, "data-ph"); key != "" {
					repl, err := data.nodes(def, key, c.DataAtom == atom.Div)
					if err != nil {
						walkErr = err
						return
					}
					for _, r := range repl {
						n.InsertBefore(r, c)
					}
					n.RemoveChild(c)
					c = next
					continue
				}
				if c.DataAtom == atom.Img {
					id, _ := MailfileId(attr(c, "src"))
					src, inline, size, err := imageSource(id, byId[id], mode)
					if err != nil {
						walkErr = err
						return
					}
					setAttr(c, "src", src)
					if inline != nil && !used[id] {
						used[id] = true
						out.Inline = append(out.Inline, inline)
						fileBytes += size
					}
				}
				walk(c)
			}
			c = next
		}
	}
	walk(root)
	if walkErr != nil {
		return nil, walkErr
	}

	if attachment != nil {
		fileBytes += int64(len(attachment.Content))
		out.Attachment = &services.Attachment{
			Type:        "DEFAULT",
			Filename:    attachment.Filename,
			Filecontent: bytes.NewBuffer(attachment.Content),
			MimeType:    attachment.MimeType,
		}
	}
	if fileBytes > def.Limits.MaxTotalBytes {
		return nil, fmt.Errorf("%w: files %d bytes, limit %d bytes", ErrTooLarge, fileBytes, def.Limits.MaxTotalBytes)
	}

	// placeholder values are text too
	breakTemplateBraces(root)
	bodyHtml, err := renderChildren(root)
	if err != nil {
		return nil, err
	}
	out.Html = frame(renderedSubject, bodyHtml)
	return out, nil
}

// FixedFooterText is the line every member mail ends with, in send, preview and
// test mail, for an own text and for the built-in default. The server appends it
// after sanitising, so it is neither editable nor part of the stored text. Same
// text in eegfaktura-billing.
const FixedFooterText = "versandt durch EEG Faktura, ein Service des VFEEG - https://vfeeg.org/"

// FixedFooterHtml is FixedFooterText as HTML.
const FixedFooterHtml = `<p style="font-size:12px;color:#6b7280">versandt durch EEG Faktura, ein Service des VFEEG - <a href="https://vfeeg.org/">https://vfeeg.org/</a></p>`

// AppendFixedFooter adds the fixed footer to a complete HTML mail: before the
// last </body>, at the end when there is none.
func AppendFixedFooter(doc string) string {
	i := lastIndexBodyEnd(doc)
	if i < 0 {
		return doc + "\n" + FixedFooterHtml
	}
	return doc[:i] + FixedFooterHtml + "\n" + doc[i:]
}

// lastIndexBodyEnd returns the byte offset of the last "</body>" in doc, in any
// ASCII case, -1 if there is none. It compares bytes, so the offset is valid
// for doc itself (strings.ToLower may change the length, e.g. of 'İ').
func lastIndexBodyEnd(doc string) int {
	const tag = "</body>"
	for i := len(doc) - len(tag); i >= 0; i-- {
		match := true
		for j := 0; j < len(tag) && match; j++ {
			b := doc[i+j]
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			match = b == tag[j]
		}
		if match {
			return i
		}
	}
	return -1
}

// frame is the fixed outer frame of every customised member mail; it matches
// the head of the legacy templates and ends with the fixed footer.
func frame(subject, body string) string {
	return AppendFixedFooter("<!DOCTYPE html>\n<html lang=\"de\">\n<head>\n    <meta charset=\"UTF-8\">\n    <title>" +
		html.EscapeString(subject) + "</title>\n</head>\n<body>\n" + body + "\n</body>\n</html>")
}

// imageSource returns the src of an image and, for ImagesInline, its inline attachment.
func imageSource(id string, f *model.MailTemplateFile, mode ImageMode) (string, *services.Attachment, int64, error) {
	var content []byte
	var mimeType, filename, cid string
	var size int64
	switch {
	case id == BuiltinLogoId:
		logo, err := public.Templates.ReadFile("templates/eegfaktura-logo.png")
		if err != nil {
			return "", nil, 0, err
		}
		content, mimeType, filename, cid = logo, "image/png", "eegfaktura-logo.png", builtinLogoCid
	case f != nil:
		content, mimeType, filename, cid = f.Content, f.MimeType, f.Filename, f.Id
		size = int64(len(f.Content))
	default:
		// SanitizeBody keeps only known images
		return "", nil, 0, fmt.Errorf("image %q not found", id)
	}
	if mode == ImagesDataURI {
		return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(content), nil, 0, nil
	}
	contentId := cid
	return "cid:" + cid, &services.Attachment{
		Type:        "INLINE",
		Filename:    filename,
		Filecontent: bytes.NewBuffer(content),
		MimeType:    mimeType,
		ContentId:   &contentId,
	}, size, nil
}

// renderSubject replaces the {key} tokens of a stored subject by plain values
// without line breaks.
func renderSubject(def *Definition, stored string, data *Data) (string, error) {
	var renderErr error
	out := subjectToken.ReplaceAllStringFunc(stored, func(token string) string {
		v, err := data.text(def, token[1:len(token)-1])
		if err != nil && renderErr == nil {
			renderErr = err
		}
		return spaces.ReplaceAllString(v, " ")
	})
	if renderErr != nil {
		return "", renderErr
	}
	return strings.TrimSpace(spaces.ReplaceAllString(out, " ")), nil
}

// text returns the plain value of a placeholder ("" when missing).
func (d *Data) text(def *Definition, key string) (string, error) {
	ph := def.Placeholder(key)
	if ph == nil {
		return "", fmt.Errorf("unknown placeholder %q", key)
	}
	key = ph.Key // an alias (placeholderAliases) renders as its key
	eeg := d.Eeg
	if eeg == nil {
		eeg = &model.Eeg{}
	}
	switch key {
	case "member.firstName":
		return d.FirstName, nil
	case "member.lastName":
		return d.LastName, nil
	case "member.number":
		return d.Number, nil
	case "meteringPoint", "meteringPoints":
		return strings.Join(d.MeteringPoints, ", "), nil
	case "gridOperator.name":
		if d.GridOperator != "" {
			return d.GridOperator, nil
		}
		return eeg.OperatorName, nil
	case "direction":
		return d.Direction, nil
	case "endDate":
		return d.EndDate, nil
	case "directionText":
		if v := strings.TrimSpace(d.Direction); v != "" {
			return "(" + v + ")", nil
		}
		return "", nil
	case "endDateText":
		if v := strings.TrimSpace(d.EndDate); v != "" {
			return "ab dem " + v, nil
		}
		return "", nil
	case "reason":
		return d.Reason, nil
	case "eeg.name":
		// the community's designation (base.eeg.description), as the issuer on documents and {eeg.name} in the
		// billing mail (decision 25.10.2026); the short name only when the designation is empty
		return eegDisplayName(eeg), nil
	case "eeg.email":
		return strings.TrimSpace(eeg.Email.String), nil
	case "eeg.phone":
		return strings.TrimSpace(eeg.Phone.String), nil
	case "eeg.website":
		return strings.TrimSpace(eeg.Website.String), nil
	case "eeg.contactBlock":
		return strings.Join(d.contactLines(), ", "), nil
	}
	return "", fmt.Errorf("placeholder %q has no value", key)
}

// nodes returns the HTML nodes that replace a placeholder element. Values are
// text nodes, so the HTML renderer escapes them.
func (d *Data) nodes(def *Definition, key string, block bool) ([]*xhtml.Node, error) {
	switch key {
	case "meteringPoints":
		if len(d.MeteringPoints) == 0 {
			return nil, nil
		}
		ul := element("ul")
		for _, m := range d.MeteringPoints {
			li := element("li")
			li.AppendChild(text(m))
			ul.AppendChild(li)
		}
		return []*xhtml.Node{ul}, nil
	case "eeg.contactBlock":
		var out []*xhtml.Node
		for _, line := range d.contactLines() {
			div := element("div")
			div.AppendChild(text(line))
			out = append(out, div)
		}
		return out, nil
	}
	v, err := d.text(def, key)
	if err != nil {
		return nil, err
	}
	if block {
		div := element("div")
		div.AppendChild(text(v))
		return []*xhtml.Node{div}, nil
	}
	return []*xhtml.Node{text(v)}, nil
}

// contactLines is the signature of the community, as in the legacy templates:
// name, contact person, address, phone, e-mail, website — each only when present.
func (d *Data) contactLines() []string {
	eeg := d.Eeg
	if eeg == nil {
		return nil
	}
	var lines []string
	add := func(prefix, v string) {
		if v = strings.TrimSpace(v); v != "" {
			lines = append(lines, prefix+v)
		}
	}
	name := eegDisplayName(eeg)
	add("", name)
	add("", eeg.ContactPerson.String)
	place := strings.TrimSpace(eeg.Zip + " " + eeg.City)
	street := strings.TrimSpace(eeg.Street + " " + eeg.StreetNumber)
	if place != "" && street != "" {
		add("", place+", "+street)
	} else {
		add("", place+street)
	}
	add("T: ", eeg.Phone.String)
	add("E: ", eeg.Email.String)
	add("", eeg.Website.String)
	return lines
}

func element(name string) *xhtml.Node {
	return &xhtml.Node{Type: xhtml.ElementNode, Data: name, DataAtom: atom.Lookup([]byte(name))}
}

func text(s string) *xhtml.Node {
	return &xhtml.Node{Type: xhtml.TextNode, Data: s}
}

func setAttr(n *xhtml.Node, key, val string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, xhtml.Attribute{Key: key, Val: val})
}

// eegDisplayName is the designation of the community, else its short name.
func eegDisplayName(eeg *model.Eeg) string {
	if strings.TrimSpace(eeg.Description) != "" {
		return eeg.Description
	}
	return eeg.Name
}

func countDistinct(ids []string) int {
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	return len(seen)
}
