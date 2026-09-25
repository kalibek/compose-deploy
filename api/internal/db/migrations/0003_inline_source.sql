-- source: 'git' (compose file from a repository) or 'inline' (compose file
-- edited in the UI and stored here in compose_content).
ALTER TABLE stacks ADD COLUMN source          TEXT NOT NULL DEFAULT 'git';
ALTER TABLE stacks ADD COLUMN compose_content TEXT NOT NULL DEFAULT '';
