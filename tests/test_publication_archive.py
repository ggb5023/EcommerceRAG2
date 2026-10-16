"""Security and resumability contracts for publication tooling."""
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts' / 'ops'))
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts' / 'check'))
from publication_scan import Scanner, sha
from publication_archive import inventory, transfer


class PublicationTests(unittest.TestCase):
    def test_redaction_keeps_nonsecret_original(self):
        scanner = Scanner()
        value = 'credential-' + 'z'*30
        scanner.learn(json.dumps({'client_secret': value}).encode())
        original = ('# Original notes\nAPI_KEY="'+value+'"\nA known failure remains FAILED\n').encode()
        exported, why = scanner.inspect(original, 'notes.md')
        self.assertNotIn(value.encode(), exported)
        self.assertIn(b'FAILED', exported)
        self.assertTrue(why)
        plain = b'# nonsecret Chinese source\n'
        self.assertEqual(scanner.inspect(plain, 'notes.md'), (plain, []))

    def test_archive_content_and_path_inspection(self):
        scanner = Scanner()
        secret = ('sk-' + 'x'*35).encode()
        def archive(name, data):
            out = io.BytesIO()
            with zipfile.ZipFile(out, 'w') as z:
                z.writestr(name, data)
            return out.getvalue()
        exported, why = scanner.inspect(archive('word/document.xml', secret), 'test.docx')
        with zipfile.ZipFile(io.BytesIO(exported)) as z:
            self.assertNotIn(secret, z.read('word/document.xml'))
        self.assertTrue(why)
        self.assertIsNone(scanner.inspect(archive('../outside', b'ok'), 'test.zip')[0])
        self.assertIsNone(scanner.inspect(archive('nested.zip', archive('binary', b'\x00')), 'outer.zip')[0])
        self.assertIsNone(scanner.inspect(b'\x89PNG\r\n', 'shot.png')[0])

    def test_inventory_symlinks_redaction_and_actual_digest(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            root = base / 'root'
            root.mkdir()
            (root / 'source.md').write_text('# original\n')
            (root / 'link').symlink_to('/etc/passwd')
            source = b'password="secret-' + b'q'*30 + b'"\n'
            (root / 'config.env').write_bytes(source)
            result = inventory({'v2':root}, base/'export')
            self.assertEqual((root/'config.env').read_bytes(), source)
            records = {r['original_path']:r for r in result['files']}
            self.assertEqual(records['link']['status'], 'PENDING')
            for r in result['files']:
                if r['status'] == 'READY':
                    content = (base/'export'/r['object_path']).read_bytes()
                    self.assertEqual(sha(content), r['export_sha256'])
                    self.assertNotIn(b'secret-' + b'q'*30, content)
            self.assertEqual(result['archive_status'], 'INCOMPLETE')

    def test_database_export_disables_sessions_preserves_links_and_json_null(self):
        from publication_db_export import sanitize_row
        from publication_db_verify import restore_sql
        scanner = Scanner()
        row = dict(id='session-one', customer_id='visitor-one', token_sha256='a'*64,
                   csrf_token='old-session-csrf', revoked_at=None, expires_at='2099-01-01',idle_until='2099-01-01')
        cleaned, methods = sanitize_row('customer_session',row,scanner)
        self.assertEqual(cleaned['customer_id'],row['customer_id'])
        self.assertNotEqual(cleaned['token_sha256'],row['token_sha256'])
        self.assertIn('2000-',cleaned['revoked_at'])
        self.assertEqual(row['csrf_token'],'old-session-csrf')
        self.assertTrue(methods)
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root/'pre-data.sql').write_bytes(b'-- pre\n')
            (root/'post-data.sql').write_bytes(b'-- post\n')
            content = (json.dumps(dict(data=dict(id=1,evidence_json=None),sql_null_columns=[]))+'\n').encode()
            (root/'sample.jsonl').write_bytes(content)
            ev = dict(table_counts=dict(sample=1), table_sha256=dict(sample=sha(content)),
                      columns=dict(sample=['id','evidence_json']),json_columns=dict(sample=['evidence_json']))
            result = b''.join(restore_sql(root,ev))
            self.assertIn(b"-> 'evidence_json'",result)
            (root/'sample.jsonl').write_bytes(b'changed')
            with self.assertRaisesRegex(ValueError,'hash mismatch'):
                b''.join(restore_sql(root,ev))

    def test_transfer_unknown_commit_retry_resume_and_conflict(self):
        class Store:
            data = None
            puts = 0
            gets = 0
            def exists(self, key):
                return self.data is not None
            def create(self, key, path, digest):
                self.puts += 1
                self.data = path.read_bytes()
                raise ConnectionError('response lost after upload')
            def digest(self, key):
                self.gets += 1
                return sha(self.data)
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)/'object'
            path.write_bytes(b'data')
            store = Store()
            self.assertEqual(transfer(store, 'batch/object', path, sha(b'data')), 'EXISTING_VERIFIED')
            self.assertEqual(store.puts, 1)
            self.assertEqual(transfer(store, 'batch/object', path, sha(b'data')), 'EXISTING_VERIFIED')
            self.assertEqual(store.puts, 1)
            store.data = b'other data'
            with self.assertRaisesRegex(ValueError, 'conflict'):
                transfer(store, 'batch/object', path, sha(b'data'))


if __name__ == '__main__':
    unittest.main()
