-- 0015_proxy_structured_fields.sql — split proxies.url_encrypted into structured
-- fields (#71).
--
-- A proxy was one encrypted composite URL, so host/port — plain, not secret — were
-- hidden along with the password. Host/port/username are now plain columns; only
-- the password stays encrypted. url_encrypted is kept (still NOT NULL, default '')
-- so the boot backfill (internal/resourcemigrate) can decrypt an existing row's
-- composite URL under the legacy AAD, split it, and clear the column back to '' —
-- the same "empty means not yet set" convention InsertProxy already uses for a
-- not-yet-sealed secret between its two write phases.

ALTER TABLE proxies ADD COLUMN host               TEXT    NOT NULL DEFAULT '';
ALTER TABLE proxies ADD COLUMN port                INTEGER NOT NULL DEFAULT 0;
ALTER TABLE proxies ADD COLUMN username            TEXT    NOT NULL DEFAULT '';
ALTER TABLE proxies ADD COLUMN password_encrypted  TEXT    NOT NULL DEFAULT '';
