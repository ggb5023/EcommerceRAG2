"""Locations survive the actual package-to-worker serialization boundary."""
import hashlib
import io
from zipfile import ZipFile

from app.ingest.worker import ClaimedFile, parse_persisted_package


def parse(raw, name, format):
    sha = hashlib.sha256(raw).hexdigest()
    manifest = ("schema_version: 1\nsource:\n  id: preview\n  name: Preview\n"
                "pipeline_version: preview-v1\ndocuments:\n  - document_id: preview\n"
                f"    title: Preview\n    path: {name}\n    format: {format}\n"
                f"    sha256: {sha}\n    disclosure_class: external_allowed\n")
    parsed, _ = parse_persisted_package(manifest, (ClaimedFile(1, 1, 'preview', name, sha, raw),),
                                      tenant='tenant-a', shop='shop-a')
    return parsed['documents'][0]['chunks']


def test_markdown_long_text_retains_element_ranges_and_offsets():
    text = '长段落。' * 300
    rows = parse(('# 商品\n\n## 护理\n' + text + '\n').encode(), 'guide.md', 'markdown')
    chunks = [r for r in rows if r['content_type'] == 'text']
    assert len(chunks) > 1
    for chunk in chunks:
        p = chunk['metadata']['source_position']
        assert (p['line_start'], p['line_end']) == (4, 4)
        assert text[p['char_start']:p['char_end']] == chunk['content']
        assert chunk['heading_path'] == '商品 / 护理'


def test_csv_multiline_faq_records_preserve_physical_lines():
    rows = parse('question,answer\n如何清洁？,"软布擦拭。\n不要浸泡。"\n'.encode(), 'faq.csv', 'csv')
    assert len(rows) == 1
    p = rows[0]['metadata']['source_position']
    assert {k: p[k] for k in ('line_start', 'line_end', 'record_number')} == {
        'line_start': 2, 'line_end': 3, 'record_number': 1}
    assert rows[0]['metadata']['table_body'] == [['question: 如何清洁？', 'answer: 软布擦拭。\n不要浸泡。']]


def test_docx_blocks_and_tables_have_no_invented_pages():
    xml = ('<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>'
           '<w:p><w:r><w:t>清洁说明</w:t></w:r></w:p><w:tbl>'
           '<w:tr><w:tc><w:p><w:r><w:t>型号</w:t></w:r></w:p></w:tc></w:tr>'
           '<w:tr><w:tc><w:p><w:r><w:t>A1</w:t></w:r></w:p></w:tc></w:tr>'
           '</w:tbl></w:body></w:document>')
    buffer = io.BytesIO()
    with ZipFile(buffer, 'w') as archive:
        archive.writestr('word/document.xml', xml)
    rows = parse(buffer.getvalue(), 'guide.docx', 'docx')
    assert rows[0]['metadata']['source_position']['block_index'] == 1
    table = next(r for r in rows if r['content_type'] == 'table')
    assert table['metadata']['source_position']['block_index'] == 2
    assert table['metadata']['source_position']['table'] == 1
    assert all('page_no' not in r['metadata']['source_position'] for r in rows)


def test_faq_and_parser_info_do_not_become_new_quality_failures():
    faq = parse('# 如何清洁？\n\n用软布擦拭。\n'.encode(), 'faq-care.md', 'markdown')
    assert faq[-1]['metadata']['source_position']['line_start'] == 3
    code = parse(b'```python\nprint(1)\n```\n', 'code.md', 'markdown')
    assert code[0]['metadata']['warning'] == 'code_language:python'
    assert code[0]['metadata']['source_position']['line_end'] == 3
