-- M6 suggestion replies (0031): hidden suggestion conversations and their turns
-- share the existing tables but must never appear in staff lists or metrics.
BEGIN;

ALTER TABLE conversation ADD COLUMN kind text NOT NULL DEFAULT 'user'
    CHECK (kind IN ('user', 'suggestion'));
ALTER TABLE turn ADD COLUMN kind text NOT NULL DEFAULT 'user'
    CHECK (kind IN ('user', 'suggestion'));

COMMENT ON COLUMN conversation.kind IS
    'user = staff conversation; suggestion = hidden M6 suggestion session (0031)';
COMMENT ON COLUMN turn.kind IS
    'user = staff turn; suggestion = hidden M6 suggestion turn (0031)';

INSERT INTO schema_migration(version,checksum_sha256) VALUES ('0045', :'migration_checksum');
COMMIT;
