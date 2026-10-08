-- Authorization for the runtime login.
--
-- Runs as notification_owner: the migrate subcommand logs in as
-- notification_migrator and switches with SET ROLE, so every object below and
-- every later one belongs to the owner. notification_runtime gets CRUD only; it
-- never owns, alters or drops.
--
-- The roles are created by the platform before migrations run (CNPG on the
-- cluster, init.sql in local-stack, the test setup in CI). A missing role
-- fails this migration on purpose.

GRANT USAGE ON SCHEMA public TO notification_runtime;

-- Objects that already exist. Granted by name, not ON ALL TABLES, so the
-- runtime never reaches schema_migrations.
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.notifications TO notification_runtime;
GRANT USAGE, SELECT ON SEQUENCE public.notifications_id_seq TO notification_runtime;

-- Every table and sequence a later migration creates.
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO notification_runtime;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO notification_runtime;

-- Global, not IN SCHEMA: a per-schema revoke cannot cancel the built-in
-- PUBLIC EXECUTE on functions.
ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;
