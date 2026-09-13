ALTER TABLE users DROP CONSTRAINT users_role_check;
UPDATE users SET role = 'developer' WHERE role = 'member';
ALTER TABLE users ALTER COLUMN role SET DEFAULT 'developer';
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin', 'developer', 'viewer'));
ALTER TABLE users DROP COLUMN IF EXISTS permissions;
