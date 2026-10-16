from __future__ import annotations

import os
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
RUNNER = ROOT / "scripts/test-migrations.sh"


class MigrationRunnerStaticTests(unittest.TestCase):
    def test_versions_are_contiguous_and_ordered(self):
        versions = sorted(path.name[:4] for path in (ROOT / "sql/migrations").glob("[0-9][0-9][0-9][0-9]_*.sql"))
        self.assertEqual(versions, [f"{number:04d}" for number in range(1, len(versions) + 1)])

    def test_new_migration_records_valid_raw_file_checksum(self):
        migration = ROOT / "sql/migrations/0004_migration_checksum.sql"
        sql = migration.read_text(encoding="utf-8")
        self.assertIn("checksum_sha256", sql)
        self.assertIn(":'migration_checksum'", sql)
        self.assertRegex(sql, r"\^\[0-9a-f\]\{64\}\$")
        self.assertIn("sha256sum", RUNNER.read_text(encoding="utf-8"))

    def test_every_post_checksum_migration_receives_the_raw_file_hash(self):
        for migration in sorted((ROOT / "sql/migrations").glob("000[5-9]_*.sql")):
            self.assertIn(":'migration_checksum'", migration.read_text(encoding="utf-8"), migration.name)

    def test_runner_reports_legacy_and_rejects_mismatch(self):
        runner = RUNNER.read_text(encoding="utf-8")
        self.assertIn("LEGACY_UNVERIFIED", runner)
        self.assertIn("FAIL checksum mismatch", runner)
        self.assertIn("M1_APP_DATABASE_URL", runner)

    def test_synthetic_authorization_constraints_are_versioned(self):
        migration = ROOT / "sql/migrations/0008_synthetic_auth_constraints.sql"
        sql = migration.read_text(encoding="utf-8")
        for fragment in (
            "app_user_role_check",
            "acl_resource_type_check",
            "acl_subject_type_check",
            "acl_permission_check",
            "acl_tenant_resource_subject_key",
            "ecr_validate_acl_scope",
            ":'migration_checksum'",
        ):
            self.assertIn(fragment, sql)
        self.assertIn("m1-auth-constraints.sql", RUNNER.read_text(encoding="utf-8"))

    def test_admin_role_catalog_is_versioned_and_read_only_for_app(self):
        migration = ROOT / "sql/migrations/0014_admin_role_catalog.sql"
        sql = migration.read_text(encoding="utf-8")
        for fragment in (
            "CREATE TABLE admin_role_catalog",
            "platform_access_admin",
            "platform_role_approver",
            "support_permission_reviewer",
            "merchant_member",
            "GRANT SELECT ON admin_role_catalog TO rag_app",
            ":'migration_checksum'",
        ):
            self.assertIn(fragment, sql)

    def test_v2_checkpoint_migration_pins_saver_schema_and_dml_grants(self):
        migration = ROOT / "sql/migrations/0015_v2_graph_checkpoint.sql"
        sql = migration.read_text(encoding="utf-8")
        for table in (
            "v2_asking_checkpoint",
            "checkpoint_migrations",
            "checkpoints",
            "checkpoint_blobs",
            "checkpoint_writes",
        ):
            expected = f"CREATE TABLE IF NOT EXISTS {table}"
            if table == "v2_asking_checkpoint":
                expected = "CREATE TABLE v2_asking_checkpoint"
            self.assertIn(expected, sql)
        self.assertIn("generate_series(0, 9)", sql)
        self.assertIn("GRANT SELECT, INSERT, UPDATE, DELETE ON checkpoints TO rag_app", sql)
        self.assertIn("VALUES ('0015', :'migration_checksum')", sql)

    def test_resume_idempotency_is_bound_to_exact_execution(self):
        migration = ROOT / "sql/migrations/0016_resume_idempotency_execution.sql"
        sql = migration.read_text(encoding="utf-8")
        for fragment in (
            "ALTER TABLE turn_idempotency",
            "ADD COLUMN execution_id BIGINT",
            "turn_idempotency_execution_fk",
            "REFERENCES conversation_execution",
            "idx_turn_idempotency_execution",
            "VALUES ('0016', :'migration_checksum')",
        ):
            self.assertIn(fragment, sql)

    def test_v2_fixture_tenant_is_isolated_from_m1_identity(self):
        migration = ROOT / "sql/migrations/0017_v2_fixture_tenant.sql"
        sql = migration.read_text(encoding="utf-8")
        for fragment in (
            "V2 LangGraph fixture tenant",
            "v2-demo-tenant",
            "dev-user",
            "v2-doc-bottle",
            "v2-doc-shipping",
            "v2-doc-internal",
            "VALUES ('0017', :'migration_checksum')",
        ):
            self.assertIn(fragment, sql)

    def test_v2_retrieval_indexes_are_versioned(self):
        migration = ROOT / "sql/migrations/0018_v2_retrieval_indexes.sql"
        sql = migration.read_text(encoding="utf-8")
        for fragment in (
            "idx_v2_document_active_scope",
            "idx_v2_chunk_scope_version",
            "idx_v2_chunk_embed_text_trgm",
            "VALUES ('0018', :'migration_checksum')",
        ):
            self.assertIn(fragment, sql)

if __name__ == "__main__":
    unittest.main()
