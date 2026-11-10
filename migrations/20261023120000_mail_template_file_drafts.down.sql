ALTER TABLE base.mail_template DROP COLUMN IF EXISTS attachment_file_id;
ALTER TABLE base.mail_template_file DROP COLUMN IF EXISTS committed;
