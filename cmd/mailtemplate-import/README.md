# mailtemplate-import

One-off operator tool. It moves the hand-made member mail templates from the data volume into the
editable mail texts (`/eeg/mailtemplates`). See konzept-mail-texte-editierbar.md, sections G.4 and C.

From backend 1.9.0 on, the backend no longer reads the volume templates (`<TENANT>/templates`
and the global `templates` dir): after the import only the saved version counts, and a community without one gets
the built-in default. The import is optional: since 2026-10-26 it is not part of the rollout, the communities set up
their own text in the editor themselves. The global `templates` dir is not imported.

## Input

A **copy of the data volume**, not the repository's `public/` folder. It uses the same layout:

```
<base>/<TENANT>/templates/activation-mail-template.toml   (also zp-complete-/zp-inactive-mail-template.toml)
<base>/<TENANT>/templates/<TemplateFile>.html
<base>/<TENANT>/templates/<InlinePictures>  <Attachment>
```

## Conversion

| Go template | new format |
|---|---|
| `{{.Participant.FirstName}}`, `.LastName`, `.ParticipantNumber.String` | `member.firstName`, `member.lastName`, `member.number` |
| `{{.Eeg.Name}}`, `{{.Eeg.Description}}`, `.Contact.Phone.String`, `.Contact.Email.String`, `.Website.String` | `eeg.name` (both: the designation since 25.10.2026), `eeg.phone`, `eeg.email`, `eeg.website` |
| `<ul>{{range .Meteringpoints}}<li>{{.}}</li>{{end}}</ul>` | block `meteringPoints` |
| several consecutive `{{if .Eeg.<field>.Valid}}…{{end}}`, or one around the contact person | block `eeg.contactBlock` |
| a single `{{if .Eeg.Contact.Phone/Email/Website.Valid}}…{{end}}` | condition dropped, inner placeholder kept |
| `<img src="cid:…">` from `InlinePictures` | upload as image, `mailfile:<id>`, size from `max-height`/`max-width` becomes `width` |
| `cid:eegfaktura-logo-1` / `eegfaktura-logo.png` | built-in `mailfile:eegfaktura-logo` (no upload) |
| closing `<p>versandt durch</p>` + `cid:eegfaktura-logo-1` at the end | dropped (Hinweis): every mail ends with the fixed footer |
| `Attachment` | **STOP** while member mails allow no PDF (since 26.10.2026) |
| `<div>` holding only text (signature lines) | its content followed by `<br>` (the policy unwraps plain divs) |

Since 25.10.2026 `{{.Eeg.Name}}` (short name) and `{{.Eeg.Description}}` both become `eeg.name`, the designation. A template
that shows both (e.g. `{{.Eeg.Description}} ({{.Eeg.Name}})`) then repeats the designation, and a short name used on
purpose turns into the long one: check the converted text in the dry run / preview before importing.

Any other template expression is a **STOP**. The mail of that tenant is not imported, so no raw
`{{…}}` ever reaches a member. A file that is not PNG/JPEG/PDF, or that is too large, is also a STOP, and
so are links the save would refuse (more than 3 links or 2 domains, URL shorteners, IP addresses, a link text
showing another address — the link rules of `mail-html-policy.json`).
Everything the sanitiser removes (fonts, colours, tables, `http:` links, external images) is printed
as a **Hinweis**. Go through these hints with the community.

Subject: the hand-made templates have none. The catalog default (`Aktivierung im Serviceportal`) is used.

## Usage

```bash
go build -o mailtemplate-import ./cmd/mailtemplate-import

# 1. dry run: converted HTML and findings per tenant, writes nothing
./mailtemplate-import -base /copy/of/volume -dry-run

# 2. import (token of a superuser who also has the /EEG_ADMIN group; never as a flag)
export MAILTPL_IMPORT_TOKEN=…
./mailtemplate-import -base /copy/of/volume -api https://<host>/api -tenants RC100181,RC100331 -test-mail eeg
```

| Flag | Meaning |
|---|---|
| `-base` | volume copy (required) |
| `-api` | backend base URL without `/eeg` (required unless `-dry-run`) |
| `-tenants` | comma-separated list; default is every `<TENANT>/templates` under `-base` |
| `-dry-run` | print only |
| `-overwrite` | replace a mail the community has already customised (default: skip it with an error) |
| `-test-mail eeg\|me` | after the import, send a test mail to the community address or to the token's e-mail |

Per mail the tool:
1. reads the current version (`GET`); a mail the community has already customised is skipped unless `-overwrite` is set;
2. uploads the images and the PDF as drafts;
3. saves with the current version and `attachmentFileId` (`PUT`), so `-overwrite` replaces the community's version
   only in this one step;
4. sends the test mail, if requested.

If a step fails, the community's saved version stays untouched and the uploaded drafts are deleted again. Without a
saved version the community gets the built-in default (the volume template is not used). The exit code is 1 if any
mail was not imported.

The volume templates are no longer used after the import; keep them only as a backup until the community has
approved the imported text (two weeks without objection, G.4), then remove them.
