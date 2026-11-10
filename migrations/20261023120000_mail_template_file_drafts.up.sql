-- Uploads are drafts until a saved version uses them; the PDF of a version is named explicitly
-- (konzept-mail-texte-editierbar.md, section H, review 22.10.2026). Additive.
ALTER TABLE base.mail_template_file ADD COLUMN IF NOT EXISTS committed BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE base.mail_template ADD COLUMN IF NOT EXISTS attachment_file_id UUID;

-- versions saved before this migration (Dev only, nothing released): their files stay part of them
UPDATE base.mail_template_file f
SET committed = true
WHERE EXISTS (SELECT 1
              FROM base.mail_template t
              WHERE t.tenant = f.tenant
                AND t.mail_key = f.mail_key
                AND (f.kind = 'attachment' OR position('mailfile:' || f.id::text IN t.body_html) > 0));
UPDATE base.mail_template t
SET attachment_file_id = (SELECT f.id
                          FROM base.mail_template_file f
                          WHERE f.tenant = t.tenant
                            AND f.mail_key = t.mail_key
                            AND f.kind = 'attachment'
                          ORDER BY f.created_at
                          LIMIT 1)
WHERE t.attachment_file_id IS NULL;
