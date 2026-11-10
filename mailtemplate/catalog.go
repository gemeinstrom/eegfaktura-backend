// Package mailtemplate holds the member mails a community can customise
// (konzept-mail-texte-editierbar.md): the catalog, the HTML sanitiser driven by
// mail-html-policy.json and the renderer that replaces placeholders by escaped
// values. EEG-provided text is never run through a template engine.
package mailtemplate

// Mail keys of the member mails sent by the backend.
const (
	KeyConsentRequest = "consent-request"
	KeyZpActive       = "zp-active"
	KeyZpInactive     = "zp-inactive"
)

// BuiltinLogoId is the image id of the "versandt durch eegfaktura" logo. It was
// part of the default text until default version 2; texts saved before still
// use it, so it stays available to every community and mail, served from the
// binary instead of the database.
const BuiltinLogoId = "eegfaktura-logo"

// builtinLogoCid is the Content-ID the legacy templates use for the logo.
const builtinLogoCid = "eegfaktura-logo-1"

// Limits are the upload and size limits of one mail (concept section G/H).
type Limits struct {
	MaxImages          int   `json:"maxImages"`
	MaxImageBytes      int64 `json:"maxImageBytes"`
	MaxAttachments     int   `json:"maxAttachments"`
	MaxAttachmentBytes int64 `json:"maxAttachmentBytes"`
	MaxTotalBytes      int64 `json:"maxTotalBytes"`
	// MaxLinks and MaxLinkDomains come from the links section of mail-html-policy.json (CheckLinks).
	MaxLinks       int `json:"maxLinks"`
	MaxLinkDomains int `json:"maxLinkDomains"`
}

const (
	// MaxSubjectChars is the maximum subject length in characters.
	MaxSubjectChars = 200
	// MaxBodyBytes is the maximum size of the body HTML.
	MaxBodyBytes = 100 * 1024
)

// member mails: at most 3 images and no PDF attachment for now (link rules
// Variante A, decided 2026-10-26)
var memberMailLimits = Limits{
	MaxImages:          3,
	MaxImageBytes:      1024 * 1024,
	MaxAttachments:     0,
	MaxAttachmentBytes: 3 * 1024 * 1024,
	MaxTotalBytes:      6 * 1024 * 1024,
	MaxLinks:           policy.Links.MaxLinks,
	MaxLinkDomains:     policy.Links.MaxLinkDomains,
}

// Placeholder is one value a mail text may contain. Block placeholders render
// several lines (<div data-ph>), the others a single value (<span data-ph>).
type Placeholder struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Block bool   `json:"block"`
}

// Definition is one entry of the mail catalog.
type Definition struct {
	Key            string
	Name           string
	Description    string
	Placeholders   []Placeholder
	Limits         Limits
	DefaultVersion int
	DefaultSubject string
	DefaultBody    string
	// LegacyConfig is the template config (toml) of the embedded default (public/templates).
	LegacyConfig string
}

// Placeholder returns the placeholder with the given key, nil if the mail does not know it.
func (d *Definition) Placeholder(key string) *Placeholder {
	if alias, ok := placeholderAliases[key]; ok {
		key = alias
	}
	for i := range d.Placeholders {
		if d.Placeholders[i].Key == key {
			return &d.Placeholders[i]
		}
	}
	return nil
}

// placeholderAliases are keys no longer offered that texts saved before may still contain.
var placeholderAliases = map[string]string{
	"eeg.description": "eeg.name", // same value since 25.10.2026 (the community's designation)
}

var (
	phFirstName    = Placeholder{Key: "member.firstName", Label: "Vorname"}
	phLastName     = Placeholder{Key: "member.lastName", Label: "Nachname"}
	phNumber       = Placeholder{Key: "member.number", Label: "Mitgliedsnummer"}
	phMeter        = Placeholder{Key: "meteringPoint", Label: "Zählpunkt"}
	phMeters       = Placeholder{Key: "meteringPoints", Label: "Zählpunkte (Liste)", Block: true}
	phGridOperator = Placeholder{Key: "gridOperator.name", Label: "Netzbetreiber"}
	phDirection    = Placeholder{Key: "direction", Label: "Richtung (Bezug/Einspeisung)"}
	phEndDate      = Placeholder{Key: "endDate", Label: "Enddatum"}
	// composite placeholders: carry their own wording, empty when the value is missing
	phDirectionText = Placeholder{Key: "directionText", Label: "(Richtung) – leer, wenn unbekannt"}
	phEndDateText   = Placeholder{Key: "endDateText", Label: "ab dem Enddatum – leer, wenn unbekannt"}
	phReason        = Placeholder{Key: "reason", Label: "Grund"}
	phEegName       = Placeholder{Key: "eeg.name", Label: "Name der EEG"}
	phEegEmail      = Placeholder{Key: "eeg.email", Label: "E-Mail der EEG"}
	phEegPhone      = Placeholder{Key: "eeg.phone", Label: "Telefon der EEG"}
	phEegWebsite    = Placeholder{Key: "eeg.website", Label: "Website der EEG"}
	phContactBlock  = Placeholder{Key: "eeg.contactBlock", Label: "Kontaktblock der EEG", Block: true}
)

// defaultSignature is the end of every default text: greeting and the community's
// contact block. "versandt durch" is the fixed footer (FixedFooterText) since
// default version 2, no longer part of the text.
const defaultSignature = `<p>Mit besten Grüßen</p>
<div data-ph="eeg.contactBlock">Kontaktblock der EEG</div>`

var catalog = []Definition{
	{
		Key:         KeyConsentRequest,
		Name:        "Freigabe-Aufforderung",
		Description: "Wird verschickt, wenn der Netzbetreiber den Eingang der Anmeldung eines Zählpunkts bestätigt. Das Mitglied wird gebeten, die Datenfreigabe zu bestätigen.",
		Placeholders: []Placeholder{phFirstName, phLastName, phNumber, phMeter, phMeters, phGridOperator,
			phEegName, phEegEmail, phEegPhone, phEegWebsite, phContactBlock},
		Limits:         memberMailLimits,
		DefaultVersion: 2,
		DefaultSubject: "Aktivierung im Serviceportal",
		DefaultBody: `<p>Hallo <span data-ph="member.firstName">Vorname</span>,</p>
<p>damit dein Zählpunkt tatsächlich Energie mit den anderen Mitgliedern austauschen kann, muss der EEG noch der Zugriff auf deine Energiedaten gewährt werden.</p>
<p>Benötigt wird das für folgenden Zählpunkt:</p>
<div data-ph="meteringPoints">Zählpunkte (Liste)</div>
<p>Welche Schritte dafür konkret ausgeführt werden müssen, findest du auf folgender Webseite:</p>
<p><a href="https://docs.eegfaktura.at/shelves/netz-betreiber-infos">https://docs.eegfaktura.at/shelves/netz-betreiber-infos</a></p>
<p>Wähle einfach deinen Netzbetreiber aus und folge der beschriebenen Vorgehensweise.</p>
<p>Wir freuen uns, dass du bei uns mitmachst!</p>
` + defaultSignature,
		LegacyConfig: "activation-mail-template.toml",
	},
	{
		Key:         KeyZpActive,
		Name:        "Zählpunkt aktiv",
		Description: "Wird verschickt, wenn die Anmeldung eines Zählpunkts abgeschlossen ist und der Zählpunkt aktiv ist.",
		Placeholders: []Placeholder{phFirstName, phLastName, phNumber, phMeter, phMeters, phGridOperator,
			phEegName, phEegEmail, phEegPhone, phEegWebsite, phContactBlock},
		Limits:         memberMailLimits,
		DefaultVersion: 2,
		DefaultSubject: "Dein Zählpunkt ist aktiv",
		DefaultBody: `<p>Hallo <span data-ph="member.firstName">Vorname</span>,</p>
<p>dein Zählpunkt <strong><span data-ph="meteringPoint">Zählpunkt</span></strong> wurde erfolgreich aktiviert. Ab sofort werden deine Energiedaten erfasst.</p>
` + defaultSignature,
		LegacyConfig: "zp-complete-mail-template.toml",
	},
	{
		Key:         KeyZpInactive,
		Name:        "Zählpunkt inaktiv",
		Description: "Wird verschickt, wenn ein Zählpunkt nicht mehr Teil der Gemeinschaft ist (Datenfreigabe beendet oder Zählpunkt abgemeldet).",
		Placeholders: []Placeholder{phFirstName, phLastName, phNumber, phMeter, phMeters, phDirection, phEndDate, phDirectionText, phEndDateText, phReason,
			phEegName, phEegEmail, phEegPhone, phEegWebsite, phContactBlock},
		Limits:         memberMailLimits,
		DefaultVersion: 2,
		DefaultSubject: "Dein Zählpunkt ist nicht mehr Teil der Energiegemeinschaft",
		DefaultBody: `<p>Hallo <span data-ph="member.firstName">Vorname</span>,</p>
<p>dein Zählpunkt <strong><span data-ph="meteringPoint">Zählpunkt</span></strong> <span data-ph="directionText">(Richtung) – leer, wenn unbekannt</span> ist <strong><span data-ph="endDateText">ab dem Enddatum – leer, wenn unbekannt</span></strong> nicht mehr Teil der Energiegemeinschaft <span data-ph="eeg.name">Name der EEG</span>.</p>
<p><span data-ph="reason">Grund</span></p>
<p>Falls das nicht beabsichtigt war oder du Fragen hast, melde dich bitte bei deiner Energiegemeinschaft.</p>
` + defaultSignature,
		LegacyConfig: "zp-inactive-mail-template.toml",
	},
}

// Catalog returns the member mails of the backend in display order.
func Catalog() []Definition {
	return catalog
}

// Lookup returns the catalog entry for a mail key, nil for an unknown key.
func Lookup(key string) *Definition {
	for i := range catalog {
		if catalog[i].Key == key {
			return &catalog[i]
		}
	}
	return nil
}

// LookupByLegacyConfig returns the catalog entry for a legacy template config name.
func LookupByLegacyConfig(name string) *Definition {
	for i := range catalog {
		if catalog[i].LegacyConfig == name {
			return &catalog[i]
		}
	}
	return nil
}

// DefaultBodyHtml returns the default text in its stored (sanitised) form, as
// the editor receives and saves it.
func (d *Definition) DefaultBodyHtml() string {
	body, err := SanitizeBody(d, d.DefaultBody, nil)
	if err != nil {
		return d.DefaultBody
	}
	return body
}
