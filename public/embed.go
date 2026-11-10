// Package public embeds the default mail templates that ship in the binary.
//
// They are the built-in default of the member mails: a community without a
// saved version (base.mail_template) gets these. Hand-made templates on the
// data volume are no longer read (see parser.embeddedTemplates); the embed
// lets a fresh deployment send the mails without anything on the PVC.
package public

import "embed"

//go:embed templates
var Templates embed.FS
