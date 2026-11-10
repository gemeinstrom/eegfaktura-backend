package main

import (
	"fmt"
	"regexp"
	"strings"

	"at.ourproject/vfeeg-backend/mailtemplate"
	"golang.org/x/net/html"
)

var (
	// a meter list as in the default template: <ul>{{range .Meteringpoints}}<li>{{.}}</li>{{end}}</ul>
	rangeList = regexp.MustCompile(`(?s)<ul>\s*\{\{-?\s*range\s+\.Meteringpoints\s*-?\}\}\s*<li>\s*\{\{-?\s*\.\s*-?\}\}\s*</li>\s*\{\{-?\s*end\s*-?\}\}\s*</ul>`)
	// an optional signature line: {{if .Eeg.<field>.Valid}} ... {{end}}
	signatureIf = regexp.MustCompile(`(?s)\{\{-?\s*if\s+\.Eeg\.(ContactPerson|Contact\.Phone|Contact\.Email|Website)\.Valid\s*-?\}\}(.*?)\{\{-?\s*end\s*-?\}\}`)
	value       = regexp.MustCompile(`\{\{-?\s*(\.[A-Za-z][A-Za-z.]*)\s*-?\}\}`)
	anyAction   = regexp.MustCompile(`\{\{.*?\}\}`)
	styleLength = regexp.MustCompile(`(?i)(max-height|max-width|height|width)\s*:\s*([0-9.]+)px`)
)

// values maps Go template fields to placeholder keys.
var values = map[string]string{
	".Participant.FirstName":                "member.firstName",
	".Participant.LastName":                 "member.lastName",
	".Participant.ParticipantNumber.String": "member.number",
	".Eeg.Name":                             "eeg.name",
	".Eeg.Description":                      "eeg.name",
	".Eeg.Contact.Phone.String":             "eeg.phone",
	".Eeg.Phone.String":                     "eeg.phone",
	".Eeg.Contact.Email.String":             "eeg.email",
	".Eeg.Email.String":                     "eeg.email",
	".Eeg.Website.String":                   "eeg.website",
	".Eeg.Optionals.Website.String":         "eeg.website",
	".MeteringPoint":                        "meteringPoint",
	".Direction":                            "direction",
	".EndDate":                              "endDate",
	".Reason":                               "reason",
}

// checkActionsInAttributes stops a template that uses a template expression
// inside a tag (e.g. href="mailto:{{.Eeg.Contact.Email.String}}"): a
// placeholder is an element and can never stand in an attribute.
func (c *Conversion) checkActionsInAttributes(s string) {
	z := html.NewTokenizer(strings.NewReader(s))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			tok := z.Token()
			for _, a := range tok.Attr {
				if strings.Contains(a.Key, "{{") || strings.Contains(a.Val, "{{") || strings.Contains(a.Val, "}}") {
					c.block("Template-Ausdruck in einem Attribut (<%s %s=%q>) lässt sich nicht als Platzhalter abbilden — Vorlage vorher von Hand anpassen",
						tok.Data, a.Key, a.Val)
				}
			}
			if strings.Contains(tok.Data, "{{") {
				c.block("Template-Ausdruck in einem Tag-Namen (<%s>)", tok.Data)
			}
		}
	}
}

// convertActions replaces the Go template actions by placeholders. Whatever it
// can not map is a blocking finding, so no raw {{…}} ever reaches a member.
func (c *Conversion) convertActions(def *mailtemplate.Definition, s string) string {
	blockPlaceholder := func(key string) string {
		ph := def.Placeholder(key)
		return fmt.Sprintf(`<div data-ph="%s">%s</div>`, key, html.EscapeString(ph.Label))
	}

	if rangeList.MatchString(s) {
		s = rangeList.ReplaceAllString(s, blockPlaceholder("meteringPoints"))
		c.note("Zählpunktliste ({{range .Meteringpoints}}) → Platzhalter meteringPoints")
	}

	// consecutive optional lines form the signature
	matches := signatureIf.FindAllStringSubmatchIndex(s, -1)
	var out strings.Builder
	last := 0
	for i := 0; i < len(matches); {
		j := i
		for j+1 < len(matches) && strings.TrimSpace(s[matches[j][1]:matches[j+1][0]]) == "" {
			j++
		}
		var fields []string
		contactPerson := false
		for k := i; k <= j; k++ {
			f := s[matches[k][2]:matches[k][3]]
			fields = append(fields, f)
			contactPerson = contactPerson || f == "ContactPerson"
		}
		out.WriteString(s[last:matches[i][0]])
		if j > i || contactPerson {
			out.WriteString(blockPlaceholder("eeg.contactBlock"))
			c.note("Signaturzeilen (%s) → Platzhalter eeg.contactBlock; er zeigt Bezeichnung, Kontaktperson, Adresse, Telefon, "+
				"E-Mail und Website der EEG (jeweils wenn vorhanden) — umgebende Signaturzeilen mit der EEG auf Doppelungen prüfen",
				strings.Join(fields, ", "))
		} else {
			out.WriteString(s[matches[i][4]:matches[i][5]])
			c.note("Bedingung {{if .Eeg.%s.Valid}} entfernt, Inhalt bleibt (leer, wenn der Wert fehlt)", fields[0])
		}
		last = matches[j][1]
		i = j + 1
	}
	out.WriteString(s[last:])
	s = out.String()

	s = value.ReplaceAllStringFunc(s, func(action string) string {
		field := value.FindStringSubmatch(action)[1]
		key, ok := values[field]
		if !ok {
			return action
		}
		ph := def.Placeholder(key)
		if ph == nil {
			c.block("%s: Platzhalter %s gibt es in der Mail %s nicht", action, key, def.Key)
			return action
		}
		return fmt.Sprintf(`<span data-ph="%s">%s</span>`, key, html.EscapeString(ph.Label))
	})

	if left := anyAction.FindAllString(s, -1); len(left) > 0 {
		c.block("nicht abbildbare Template-Ausdrücke: %s", strings.Join(unique(left), " "))
	} else if strings.Contains(s, "{{") || strings.Contains(s, "}}") {
		c.block("unvollständiger Template-Ausdruck ({{ oder }})")
	}
	return s
}
