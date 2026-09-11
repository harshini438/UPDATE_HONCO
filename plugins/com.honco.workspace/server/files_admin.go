package main

// Files diagnostic for the admin dashboard.
//
// Read-only, and counts only. This reports how many files Mattermost is
// holding, how many are still referenced by something, and how many are
// not -- so an administrator can see whether storage is drifting from what
// the product believes it has. It deletes nothing, and it never returns a
// path: the numbers are what an operator needs to decide, and the paths
// are what they do not need to be shown to a browser.
//
// Retention is deliberately not implemented here. Mattermost's own Data
// Retention is an Enterprise feature and is inert on this edition, so
// nothing on this server deletes a file today; the conservative policy an
// operator might adopt is written up in the Phase 8 report, and the
// numbers below are its dry run.

type adminFiles struct {
	// Everything Mattermost's fileinfo table holds that is not deleted.
	StoredFiles int   `json:"stored_files"`
	StoredBytes int64 `json:"stored_bytes"`

	// Files that are attached to a live post. Ordinary chat attachments.
	AttachedToPost int `json:"attached_to_post"`

	// Files that are meeting recordings the plugin knows about.
	Recordings       int   `json:"recordings"`
	RecordingBytes   int64 `json:"recording_bytes"`
	LargestFileBytes int64 `json:"largest_file_bytes"`

	// Referenced by nothing at all: no live post, no recording row. These
	// are the only candidates for "orphan". Candidates, not verdicts: a
	// file uploaded moments ago and not yet attached to a post also
	// counts, which is why nothing here is deleted automatically.
	PossibleOrphans int   `json:"possible_orphans"`
	OrphanBytes     int64 `json:"orphan_bytes"`

	// Attachments whose post was deleted but whose file was not. Mattermost
	// normally soft-deletes both together; a non-zero count here means
	// something bypassed that.
	PostDeletedFileKept int `json:"post_deleted_file_kept"`

	// The reverse problem: a recording the plugin marked ready whose
	// Mattermost file row is missing or deleted. The meeting card already
	// shows these as unavailable; this counts them.
	RecordingsMissingFile int `json:"recordings_missing_file"`

	// Soft-deleted rows Mattermost is still carrying. Their bytes may or
	// may not still be on disk; that is Mattermost's bookkeeping.
	SoftDeleted int `json:"soft_deleted"`

	// The limit that applies to a browser upload, and the one that applies
	// to a recording, so an operator can see when they differ.
	MaxFileBytes      int64 `json:"max_file_bytes"`
	MaxRecordingBytes int64 `json:"max_recording_bytes"`
	PublicLinks       bool  `json:"public_links_enabled"`
}

// AdminFiles gathers the counts. One statement for the fileinfo side and
// two small ones for the recording side; every predicate is over indexed
// or tiny sets.
func (s *Store) AdminFiles() (*adminFiles, error) {
	var f adminFiles
	err := s.db.QueryRow(`
		SELECT
			count(*) FILTER (WHERE f.deleteat = 0),
			coalesce(sum(f.size) FILTER (WHERE f.deleteat = 0), 0),
			count(*) FILTER (WHERE f.deleteat = 0 AND p.id IS NOT NULL AND p.deleteat = 0),
			count(*) FILTER (WHERE f.deleteat = 0 AND r.id IS NOT NULL),
			coalesce(sum(f.size) FILTER (WHERE f.deleteat = 0 AND r.id IS NOT NULL), 0),
			coalesce(max(f.size) FILTER (WHERE f.deleteat = 0), 0),
			count(*) FILTER (WHERE f.deleteat = 0 AND (p.id IS NULL OR p.deleteat > 0) AND r.id IS NULL),
			coalesce(sum(f.size) FILTER (WHERE f.deleteat = 0 AND (p.id IS NULL OR p.deleteat > 0) AND r.id IS NULL), 0),
			count(*) FILTER (WHERE f.deleteat = 0 AND p.id IS NOT NULL AND p.deleteat > 0),
			count(*) FILTER (WHERE f.deleteat > 0)
		FROM fileinfo f
		LEFT JOIN posts p ON p.id = f.postid
		LEFT JOIN honco_recordings r ON r.file_id = f.id
	`).Scan(&f.StoredFiles, &f.StoredBytes, &f.AttachedToPost, &f.Recordings, &f.RecordingBytes,
		&f.LargestFileBytes, &f.PossibleOrphans, &f.OrphanBytes, &f.PostDeletedFileKept, &f.SoftDeleted)
	if err != nil {
		return nil, err
	}

	if err := s.db.QueryRow(`
		SELECT count(*) FROM honco_recordings r
		LEFT JOIN fileinfo f ON f.id = r.file_id
		WHERE r.status = 'ready' AND r.file_id <> '' AND (f.id IS NULL OR f.deleteat > 0)
	`).Scan(&f.RecordingsMissingFile); err != nil {
		return nil, err
	}
	return &f, nil
}
