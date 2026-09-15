package main

import (
	"fmt"
	"strings"
)

// The Files browser.
//
// Mattermost has no "recent files" endpoint: its only file listing is
// POST /api/v4/files/search, which rejects an empty `terms` outright
// (api4/file.go: SetInvalidParam("terms")). So a browse-by-recency view
// has to be a query, and this is it.
//
// It is a READ over Mattermost's own tables -- fileinfo, posts, channels,
// users. Honco stores no file, copies no file and serves no file: the
// browser downloads through /api/v4/files/{id} exactly as it does from a
// message, so Mattermost re-checks permission at download time and every
// existing upload/preview/download path is untouched.
//
// Authorization is not invented here either. The caller's channel list
// comes from searchScope, which asks Mattermost's own
// Channel.ListForTeamForUser -- the same gate Global Search uses. A file
// in a channel the caller is not a member of is not in the scope, so it
// cannot be in the result, and no id or URL for it ever reaches the
// browser.

// recentFilesMaxLimit caps one page. The panel asks for 25; a caller
// asking for more gets this.
const recentFilesMaxLimit = 100

// RecentFile is one row of the Files tab.
//
// Only what the list renders: no storage path, no thumbnail path, no
// minipreview bytes. The id is Mattermost's own file id -- the same
// capability the user already holds via the message the file is attached
// to, and which Mattermost authorizes again on download.
type RecentFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Extension string `json:"extension"`
	MimeType  string `json:"mime_type"`
	Kind      string `json:"kind"`
	Size      int64  `json:"size"`
	CreatedAt int64  `json:"created_at"`

	UploaderID   string `json:"uploader_id"`
	Uploader     string `json:"uploader"`
	ChannelID    string `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	ChannelType  string `json:"channel_type"`
	PostID       string `json:"post_id"`
	HasThumbnail bool   `json:"has_thumbnail"`
}

// RecentFiles lists files attached to live posts in the given channels,
// newest first.
//
// It filters on fileinfo.channelid rather than the post's, because that
// column carries the (channelid, createat) index this query is shaped for
// -- and it is not a second source of truth: every live row in this
// database has it set and equal to its post's channel. The join to posts
// is still there to drop files whose post was deleted, and files never
// attached to a post at all (a cancelled upload), neither of which is
// something to hand back to a user.
//
// An empty channel list returns nothing rather than everything -- the
// failure mode of a scope that could not be resolved must be "show less".
func (s *Store) RecentFiles(channelIDs []string, limit, offset int) ([]*RecentFile, error) {
	if len(channelIDs) == 0 {
		return []*RecentFile{}, nil
	}
	if limit <= 0 || limit > recentFilesMaxLimit {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}

	in, args := placeholders(1, channelIDs)
	limArg := fmt.Sprintf("$%d", len(channelIDs)+1)
	offArg := fmt.Sprintf("$%d", len(channelIDs)+2)
	args = append(args, limit, offset)

	rows, err := s.db.Query(`
		SELECT f.id, coalesce(f.name, ''), coalesce(f.extension, ''),
		       coalesce(f.mimetype, ''), coalesce(f.size, 0), coalesce(f.createat, 0),
		       coalesce(f.creatorid, ''), coalesce(u.username, ''),
		       coalesce(f.channelid, ''), coalesce(c.displayname, ''),
		       -- channels.type is the channel_type ENUM, so it is cast to
		       -- text before coalesce: '' is not a member of that enum and
		       -- Postgres rejects the literal outright.
		       coalesce(c.type::text, ''),
		       coalesce(f.postid, ''), coalesce(f.haspreviewimage, false)
		FROM fileinfo f
		JOIN posts p ON p.id = f.postid AND p.deleteat = 0
		JOIN channels c ON c.id = f.channelid AND c.deleteat = 0
		LEFT JOIN users u ON u.id = f.creatorid
		WHERE f.deleteat = 0
		  AND f.archived = false
		  AND f.channelid IN (`+in+`)
		ORDER BY f.createat DESC, f.id DESC
		LIMIT `+limArg+` OFFSET `+offArg, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*RecentFile{}
	for rows.Next() {
		var f RecentFile
		if err := rows.Scan(&f.ID, &f.Name, &f.Extension, &f.MimeType, &f.Size, &f.CreatedAt,
			&f.UploaderID, &f.Uploader, &f.ChannelID, &f.ChannelName, &f.ChannelType,
			&f.PostID, &f.HasThumbnail); err != nil {
			return nil, err
		}
		f.Kind = fileKind(f.MimeType, f.Extension)
		out = append(out, &f)
	}
	return out, rows.Err()
}

// fileKind reduces a mime type and extension to the short word the panel
// shows. It is a label, not a security decision: nothing is served from
// here, so a wrong guess costs an icon and nothing else.
func fileKind(mime, ext string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case mime == "application/pdf" || ext == "pdf":
		return "pdf"
	}
	switch ext {
	case "doc", "docx", "odt", "rtf", "txt", "md":
		return "document"
	case "xls", "xlsx", "ods", "csv":
		return "spreadsheet"
	case "ppt", "pptx", "odp":
		return "presentation"
	case "zip", "gz", "tar", "7z", "rar":
		return "archive"
	case "go", "js", "ts", "py", "sh", "json", "yaml", "yml", "sql", "html", "css":
		return "code"
	}
	return "file"
}
