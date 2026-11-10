package model

import "time"

// MailTemplate is the customised version of a member mail of one community
// (base.mail_template, konzept-mail-texte-editierbar.md). Subject and BodyHtml
// are stored sanitised; placeholders are <span|div data-ph="key">.
type MailTemplate struct {
	Tenant                 string    `json:"-" db:"tenant"`
	MailKey                string    `json:"-" db:"mail_key"`
	Subject                string    `json:"-" db:"subject"`
	BodyHtml               string    `json:"-" db:"body_html"`
	Version                int       `json:"-" db:"version"`
	DefaultVersion         int       `json:"-" db:"default_version"`
	NotifiedDefaultVersion int       `json:"-" db:"notified_default_version"`
	UpdatedAt              time.Time `json:"-" db:"updated_at"`
	UpdatedBy              string    `json:"-" db:"updated_by"`
	// AttachmentFileId is the PDF of this version, nil for none.
	AttachmentFileId *string `json:"-" db:"attachment_file_id"`
}

// MailTemplateFile is an image or the PDF attachment of a member mail of one
// community (base.mail_template_file). Content is only loaded when needed.
type MailTemplateFile struct {
	Id        string    `json:"id" db:"id"`
	Tenant    string    `json:"-" db:"tenant"`
	MailKey   string    `json:"-" db:"mail_key"`
	Kind      string    `json:"kind" db:"kind"`
	Filename  string    `json:"filename" db:"filename"`
	MimeType  string    `json:"mimeType" db:"mime_type"`
	Size      int64     `json:"size" db:"size"`
	Content   []byte    `json:"-" db:"content"`
	CreatedAt time.Time `json:"-" db:"created_at"`
	// Committed files belong to the saved version; uploads are drafts and never sent.
	Committed bool `json:"committed" db:"committed"`
}

const (
	MailFileKindImage      = "image"
	MailFileKindAttachment = "attachment"
)
