ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD COLUMN IF NOT EXISTS permissions text[] NOT NULL DEFAULT '{}';

-- Grandfather existing non-admin users: they had full access before permissions
-- existed, so give them every permission instead of silently locking them out.
UPDATE users SET role = 'member' WHERE role IN ('developer', 'viewer');
UPDATE users
SET permissions = ARRAY['messages:view','messages:send','messages:schedule','instances:manage','contacts:manage']
WHERE role = 'member';

ALTER TABLE users ALTER COLUMN role SET DEFAULT 'member';
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin', 'member'));
