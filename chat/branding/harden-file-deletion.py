#!/usr/bin/env python3
"""Honco Chat: one targeted file-security fix on top of upstream Mattermost,
found by the Phase 8 files audit. Same idempotent pattern as
harden-session-security.py: safe to re-run after an upstream pull, does
nothing on a tree that is already patched.

A deleted post's attachment stayed downloadable for up to 30 minutes.

   Deleting a post soft-deletes its FileInfo rows (DeleteAt is set, and the
   database says so immediately). But the download handler, api4/file.go's
   getFile, reads the row through the local cache layer:

       Store().FileInfo().GetByIds([]string{id}, true, true, false)
                                                     ^^^^ allowFromCache

   The cache layer keys by-id entries as "<fileId>_<includeDeleted>" with a
   30-minute TTL (localcachelayer: FileInfoCacheSec = 30*60). Post deletion
   calls InvalidateFileInfosForPostCache, which clears the per-POST keys
   ("<postId>", "<postId>_deleted") -- and never the per-FILE key that
   getFile just read. So the cached FileInfo still says DeleteAt == 0, the
   `if fileInfo.DeleteAt != 0` refusal never fires, and any channel member
   who has the file id can keep downloading a deleted attachment until the
   entry expires. Measured on this server: still 200 at +20s for both the
   uploader and another member, with fileinfo.deleteat already set.

   The exposure is bounded -- channel membership is still enforced, so a
   non-member is refused throughout -- but "delete" is supposed to mean
   delete, and a file's authorization must not be decided from stale data.

   The fix is one argument: read the FileInfo with allowFromCache=false in
   getFile. Cost is a single primary-key lookup per download, which is
   nothing next to reading the file itself off disk. Scoped deliberately to
   the download handler; the cache stays in place for everything else.
"""
import os

ROOT = os.path.expanduser("~/honco-chat/server")
changed = []

p = os.path.join(ROOT, "server/channels/api4/file.go")
with open(p, encoding="utf-8") as f:
    src = f.read()

OLD = ("fileInfos, storeErr := c.App.Srv().Store().FileInfo()"
       ".GetByIds([]string{c.Params.FileId}, true, true, false)")
NEW = ("// Honco: read this without the by-id cache. Post deletion only\n"
       "\t// invalidates the per-post keys, so a cached entry can still say a\n"
       "\t// deleted file is live for up to 30 minutes. A download's\n"
       "\t// authorization must come from the database, not from that.\n"
       "\tfileInfos, storeErr := c.App.Srv().Store().FileInfo()"
       ".GetByIds([]string{c.Params.FileId}, true, false, false)")

if NEW.splitlines()[-1].strip() in src:
    print("channels/api4/file.go: already patched")
elif OLD in src:
    src = src.replace(OLD, NEW, 1)
    with open(p, "w", encoding="utf-8") as f:
        f.write(src)
    changed.append("channels/api4/file.go: getFile reads FileInfo uncached")
    print("patched channels/api4/file.go")
else:
    print("WARNING: channels/api4/file.go: expected getFile GetByIds call not found -- "
          "upstream may have changed; re-audit before assuming this is fixed")

print("changed:", changed if changed else "nothing")
