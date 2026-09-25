-- deploy_trigger: 'push' (deploy every new commit) or 'workflow' (deploy a
-- commit only after its GitHub Actions workflow run(s) succeeded).
ALTER TABLE stacks ADD COLUMN deploy_trigger TEXT NOT NULL DEFAULT 'push';
-- Optional workflow to wait for (name or file, e.g. "build" or "build.yml"); '' = all.
ALTER TABLE stacks ADD COLUMN workflow_name  TEXT NOT NULL DEFAULT '';
-- Commit that is waiting for its workflow, and why (shown in the UI).
ALTER TABLE stacks ADD COLUMN waiting_sha    TEXT NOT NULL DEFAULT '';
ALTER TABLE stacks ADD COLUMN waiting_reason TEXT NOT NULL DEFAULT '';
