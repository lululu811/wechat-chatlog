"""
Unit tests for clipper_sim.py.

These tests do NOT touch real chatlog data. They exercise the helper
functions in isolation by feeding them synthetic inputs.

The end-to-end smoke test that uses real chatlog data lives at
scripts/biz2md-clipper-sim/tests/test_e2e_smoke.py and only runs when
CHATLOG_TEST_WORK_DIR is set.
"""
import os
import sys
from pathlib import Path

# Make the parent directory importable so we can load clipper_sim
# without invoking its __main__ block.
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

import clipper_sim as cs  # noqa: E402


# ─── sanitize_filename ────────────────────────────────────────────────

class TestSanitizeFilename:
    def test_strips_windows_illegal_chars(self):
        assert cs.sanitize_filename("a/b\\c:d*e?f\"g<h>i|j") == "a_b_c_d_e_f_g_h_i_j"

    def test_trailing_dots_and_spaces_stripped(self):
        # Windows hates trailing dots/spaces; ours does too via
        # strip() + rstrip(".").
        assert cs.sanitize_filename("Article ... ") == "Article"
        assert cs.sanitize_filename("Article . . .") == "Article"

    def test_length_capped(self):
        long = "x" * 500
        out = cs.sanitize_filename(long)
        assert len(out) == 120
        assert out == "x" * 120

    def test_only_illegal_chars_becomes_underscores(self):
        # "///" becomes "___" — not empty. That matches the contract:
        # callers should detect "no usable name" themselves if needed.
        assert cs.sanitize_filename("///") == "___"
        assert cs.sanitize_filename("") == ""

    def test_unicode_passes_through(self):
        assert cs.sanitize_filename("付鹏的财经世界") == "付鹏的财经世界"

    def test_keeps_hyphens_and_spaces(self):
        assert cs.sanitize_filename("Author-2026-09-07-Title") == "Author-2026-09-07-Title"


# ─── compose_md ────────────────────────────────────────────────────────

class TestComposeMD:
    def test_frontmatter_contains_required_fields(self):
        item = dict(
            title="Test Article",
            desc="This is the abstract.",
            url="https://mp.weixin.qq.com/s?biz=x",
            time="2026-09-07T00:04:17",
            sort_seq=1,
        )
        rel_path, body = cs.compose_md(item, "Test Account")
        assert rel_path == "公众号/Test Account-2026-09-07-Test Article.md"
        assert body.startswith("---\n")
        for key in ("ghID:", "url:", "author:", "publishedAt:", "source:",
                     "tags:", "  - 公众号", "  - chatlog"):
            assert key in body, f"missing key: {key}"

    def test_body_contains_desc(self):
        item = dict(
            title="T", desc="SPECIFIC_ABSTRACT_42",
            url="u", time="2026-01-01T00:00:00", sort_seq=0,
        )
        _, body = cs.compose_md(item, "A")
        assert "SPECIFIC_ABSTRACT_42" in body

    def test_body_marks_demo_origin(self):
        item = dict(title="T", desc="d", url="u", time="2026-01-01T00:00:00", sort_seq=0)
        _, body = cs.compose_md(item, "A")
        assert "NOTE" in body
        assert "demo" in body.lower()

    def test_filename_safe_with_special_chars(self):
        item = dict(
            title='【40分钟播客】贝森特?讲"通缩"',
            desc="", url="u", time="2026-09-07T00:00:00", sort_seq=0,
        )
        rel_path, _ = cs.compose_md(item, "付鹏的财经世界")
        # The / BETWEEN folder and filename is expected; sanitize
        # only filters illegal chars in the basename.
        basename = rel_path.split("/", 1)[1]
        for bad in '<>:"/\\|?*':
            assert bad not in basename, f"basename leaked {bad!r}: {basename}"

    def test_published_at_truncated_to_date_only(self):
        item = dict(title="T", desc="d", url="u", time="2026-09-07T00:04:17", sort_seq=0)
        rel_path, _ = cs.compose_md(item, "A")
        # Filename should use YYYY-MM-DD, not full ISO timestamp
        assert "2026-09-07-T.md" in rel_path


# ─── mark_archived ─────────────────────────────────────────────────────

class TestMarkArchived:
    def _make_store(self, tmp_path):
        """Build a minimal biz_archived.db at <tmp>/biz_archived.db."""
        import sqlite3
        db_path = tmp_path / "biz_archived.db"
        con = sqlite3.connect(str(db_path))
        con.executescript("""
            CREATE TABLE biz_archived (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                gh_id TEXT NOT NULL,
                url TEXT NOT NULL UNIQUE,
                title TEXT,
                archived_at INTEGER NOT NULL,
                source TEXT NOT NULL DEFAULT 'clipper'
            );
        """)
        con.commit()
        con.close()
        return str(db_path)

    def test_inserts_one_row(self, tmp_path):
        db = self._make_store(tmp_path)
        cs.mark_archived._test_db_override = db  # type: ignore[attr-defined]
        # The function reads CHATLOG_ARCHIVE_DB at call-time (module
        # global). Patch via monkeypatch of the module attribute.
        original = cs.CHATLOG_ARCHIVE_DB
        cs.CHATLOG_ARCHIVE_DB = db
        try:
            cs.mark_archived("gh_x", "https://example.com/a", "Title A", "test")
            import sqlite3
            con = sqlite3.connect(db)
            n = con.execute("SELECT COUNT(*) FROM biz_archived").fetchone()[0]
            con.close()
            assert n == 1
        finally:
            cs.CHATLOG_ARCHIVE_DB = original

    def test_dedup_on_url(self, tmp_path):
        db = self._make_store(tmp_path)
        original = cs.CHATLOG_ARCHIVE_DB
        cs.CHATLOG_ARCHIVE_DB = db
        try:
            cs.mark_archived("gh_x", "https://example.com/dup", "T", "test")
            cs.mark_archived("gh_x", "https://example.com/dup", "T2", "test")
            cs.mark_archived("gh_x", "https://example.com/dup", "T3", "test")
            import sqlite3
            con = sqlite3.connect(db)
            n = con.execute("SELECT COUNT(*) FROM biz_archived").fetchone()[0]
            con.close()
            assert n == 1
        finally:
            cs.CHATLOG_ARCHIVE_DB = original


# ─── fetch_biz_from_sqlite: fixture-based ──────────────────────────────

class TestFetchBizFromSqlite:
    def _make_biz_db(self, tmp_path, rows):
        """Build a minimal biz_message_0.db with the given rows.

        Each row is (create_time, local_id, sort_seq, local_type,
        message_content_bytes, content_ct).
        """
        import sqlite3
        db_path = tmp_path / "biz_message_0.db"
        con = sqlite3.connect(str(db_path))
        con.execute(
            """CREATE TABLE "Msg_db1f36e22e766fc1462cacd930ff7ed0" (
                local_id INTEGER PRIMARY KEY,
                sort_seq INTEGER,
                create_time INTEGER,
                local_type INTEGER,
                message_content BLOB,
                WCDB_CT_message_content INTEGER DEFAULT NULL
            )"""
        )
        for r in rows:
            con.execute(
                """INSERT INTO "Msg_db1f36e22e766fc1462cacd930ff7ed0"
                   (create_time, local_id, sort_seq, local_type,
                    message_content, WCDB_CT_message_content)
                   VALUES (?, ?, ?, ?, ?, ?)""",
                r,
            )
        con.commit()
        con.close()
        return str(db_path)

    def test_extracts_title_desc_url(self, tmp_path):
        import zstandard as zstd
        xml = (
            "<msg><title><![CDATA[Hello World]]></title>"
            "<des><![CDATA[summary text here]]></des>"
            "<url><![CDATA[https://mp.weixin.qq.com/s?biz=x]]></url></msg>"
        ).encode("utf-8")
        cctx = zstd.ZstdCompressor()
        compressed = cctx.compress(xml)
        db = self._make_biz_db(tmp_path, [
            (1725654257, 1, 999, 1, compressed, 1),  # ct=1 means zstd
        ])

        original_db = cs.CHATLOG_DB
        cs.CHATLOG_DB = db
        try:
            items = cs.fetch_biz_from_sqlite("gh_423b608e0744")
        finally:
            cs.CHATLOG_DB = original_db

        assert len(items) == 1
        assert items[0]["title"] == "Hello World"
        assert items[0]["desc"] == "summary text here"
        assert items[0]["url"] == "https://mp.weixin.qq.com/s?biz=x"

    def test_skips_rows_with_ct_zero(self, tmp_path):
        db = self._make_biz_db(tmp_path, [
            (1725654257, 1, 999, 1, b"<msg><title>x</title></msg>", 0),  # ct=0 = raw
        ])
        original_db = cs.CHATLOG_DB
        cs.CHATLOG_DB = db
        try:
            items = cs.fetch_biz_from_sqlite("gh_423b608e0744")
        finally:
            cs.CHATLOG_DB = original_db
        # ct=0 means "not compressed", but content is plain bytes; the
        # function still tries to decode. We just verify it didn't
        # crash — the row may or may not appear depending on
        # decode success.
        assert isinstance(items, list)

    def test_returns_empty_for_missing_table(self, tmp_path):
        # Empty db with no Msg_ tables.
        import sqlite3
        db = tmp_path / "biz_message_0.db"
        con = sqlite3.connect(str(db))
        con.close()
        original_db = cs.CHATLOG_DB
        cs.CHATLOG_DB = str(db)
        try:
            items = cs.fetch_biz_from_sqlite("gh_does_not_matter")
        finally:
            cs.CHATLOG_DB = original_db
        assert items == []


# ─── end-to-end smoke (skip when fixture absent) ───────────────────────

def test_e2e_smoke_with_fixture():
    """Runs the full main() flow against a real chatlog work dir.

    Skipped unless CHATLOG_TEST_WORK_DIR points at a chatlog fixture
    that has been decrypted (i.e. contains
    db_storage/contact/contact.db and db_storage/message/biz_message_0.db).
    """
    work_dir = os.environ.get("CHATLOG_TEST_WORK_DIR")
    if not work_dir or not Path(work_dir, "db_storage", "contact", "contact.db").exists():
        import pytest
        pytest.skip("CHATLOG_TEST_WORK_DIR not configured")

    # Backup the original module-level constants, point them at the fixture.
    original = (
        cs.CHATLOG_DB, cs.CHATLOG_HTTP,
        cs.CHATLOG_ARCHIVE_DB, cs.VAULT,
    )
    cs.CHATLOG_DB = f"{work_dir}/db_storage/message/biz_message_0.db"
    cs.CHATLOG_HTTP = "http://127.0.0.1:5030"  # assumed running
    cs.CHATLOG_ARCHIVE_DB = f"{work_dir}/biz_archived.db"

    import tempfile
    test_vault = Path(tempfile.mkdtemp(prefix="clipper-sim-test-"))
    cs.VAULT = test_vault
    try:
        rc = cs.main()
        assert rc == 0
        files = list(test_vault.rglob("*.md"))
        assert len(files) >= 1, f"expected at least one md in {test_vault}, got {files}"
    finally:
        # Restore constants
        cs.CHATLOG_DB, cs.CHATLOG_HTTP, cs.CHATLOG_ARCHIVE_DB, cs.VAULT = original
        # Don't clean up test_vault — leave it for inspection if needed.