#!/usr/bin/env bash
# Honco Chat -- back up (and restore) the Postgres database and the small
# pieces of runtime state that aren't in it.
#
#   backup-honcochat.sh backup                 make one backup, prune old ones
#   backup-honcochat.sh list                   show what's on disk
#   backup-honcochat.sh restore <file> [dbname]  restore into a NEW database
#                                                 (never overwrites the live one)
#
# Nothing here talks to the network. It's pg_dump + a couple of file copies
# into $ROOT/backups, meant to be driven by cron (or a systemd timer, see the
# bottom of this file) and, ideally, synced off-box by whatever already syncs
# the rest of ubuntu3 -- that part is deliberately not this script's job.
set -euo pipefail

ROOT="${ROOT:-$HOME/honco-chat}"
PGBIN="${PGBIN:-$HOME/toolchain/pg16/bin}"
[ -x "$PGBIN/pg_dump" ] || PGBIN="/usr/lib/postgresql/16/bin"   # apt-installed dev boxes
PGHOST="${PGHOST:-127.0.0.1}"
PGPORT="${PGPORT:-5433}"
PGUSER="${PGUSER:-honco}"
PGDATABASE="${PGDATABASE:-honcochat}"

BACKUP_DIR="$ROOT/backups"
LOG="$ROOT/logs/backup.log"
KEEP="${KEEP:-14}"   # daily backups to retain; older ones are pruned

log() { printf '%s %s\n' "$(date -Is)" "$*" | tee -a "$LOG"; }

do_backup() {
    [ -x "$PGBIN/pg_dump" ] || { echo "pg_dump not found (looked in $PGBIN)" >&2; exit 1; }
    mkdir -p "$BACKUP_DIR" "$ROOT/logs"

    STAMP="$(date +%Y%m%d-%H%M%S)"
    OUT="$BACKUP_DIR/honcochat-$STAMP.dump"
    TMP="$OUT.tmp"

    log "backup: starting -> $OUT"
    # Custom format (-Fc): compressed, and restorable with pg_restore into a
    # database with a different name -- which is exactly what `restore` below
    # does, on purpose, so a bad restore can never clobber the live database.
    if ! "$PGBIN/pg_dump" -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -Fc -f "$TMP" "$PGDATABASE" 2>>"$LOG"; then
        rm -f "$TMP"
        log "backup: pg_dump FAILED -- see $LOG"
        exit 1
    fi
    mv "$TMP" "$OUT"
    SIZE=$(du -h "$OUT" | cut -f1)
    log "backup: pg_dump ok ($SIZE)"

    # Small, not-in-the-database state that a restore would otherwise lose:
    # pending /meet reminders and (if present) the meetsvc bot/command tokens.
    # These are tiny; tar them alongside the dump rather than as separate files.
    SIDECAR="$BACKUP_DIR/honcochat-$STAMP.state.tar"
    if [ -d "$ROOT/run" ]; then
        tar -cf "$SIDECAR" -C "$ROOT" \
            --ignore-failed-read \
            run/meet-schedule.json run/meetsvc.env run/.cmd-token 2>>"$LOG" || true
        [ -s "$SIDECAR" ] && log "backup: state sidecar -> $SIDECAR" || rm -f "$SIDECAR"
    fi

    # Everything else a restore would need and the dump does not contain:
    #
    #   run/data/            every attachment and every meeting recording --
    #                        Mattermost's file store (FileSettings.Directory).
    #                        A database restored without this has rows that
    #                        point at files that are not there.
    #   server/server/config/config.json
    #                        Mattermost's config, which holds every setting
    #                        including the plugin's (secrets among them --
    #                        hence the 0600 on the archive below). It lives
    #                        in the source checkout because that is where
    #                        the server's default config search finds it;
    #                        the checkout's .gitignore excludes it.
    #   run/plugins/         the installed Honco plugin (server side)
    #   run/client-plugins/  and its webapp bundle.
    #   ~/.jitsi-meet-cfg    Jitsi/Jibri configuration and finalize.sh.
    #   ~/.honco-*           the callback / service secret files.
    #
    # Mode 0600, because this archive is the one thing on disk that holds
    # every secret at once. Skipped, not failed, when FILES=0.
    if [ "${FILES:-1}" != "0" ]; then
        FILESTAR="$BACKUP_DIR/honcochat-$STAMP.files.tar.gz"
        (umask 077; tar -czf "$FILESTAR" \
            --ignore-failed-read \
            -C "$ROOT" run/data run/plugins run/client-plugins server/server/config/config.json \
            -C "$HOME" .jitsi-meet-cfg .honco-jibri-secret .honco-meet-service-secret \
            2>>"$LOG") || true
        if [ -s "$FILESTAR" ]; then
            chmod 600 "$FILESTAR"
            log "backup: files archive -> $FILESTAR ($(du -h "$FILESTAR" | cut -f1), mode 600)"
        else
            rm -f "$FILESTAR"; log "backup: files archive skipped (nothing to archive)"
        fi
    fi

    # Prune: keep the newest $KEEP dumps, drop the rest (and their sidecars).
    mapfile -t OLD < <(ls -1t "$BACKUP_DIR"/honcochat-*.dump 2>/dev/null | tail -n +$((KEEP + 1)))
    for f in "${OLD[@]:-}"; do
        [ -n "$f" ] || continue
        rm -f "$f" "${f%.dump}.state.tar" "${f%.dump}.files.tar.gz"
        log "backup: pruned $(basename "$f")"
    done
}

do_list() {
    ls -lh "$BACKUP_DIR"/honcochat-*.dump 2>/dev/null || echo "no backups yet in $BACKUP_DIR"
}

do_restore() {
    FILE="${1:?usage: backup-honcochat.sh restore <dump-file> [target-dbname]}"
    TARGET="${2:-honcochat_restore_$(date +%Y%m%d%H%M%S)}"
    [ -f "$FILE" ] || { echo "not a file: $FILE" >&2; exit 1; }
    [ -x "$PGBIN/pg_restore" ] || { echo "pg_restore not found (looked in $PGBIN)" >&2; exit 1; }

    if [ "$TARGET" = "$PGDATABASE" ]; then
        echo "refusing to restore over the live database ($PGDATABASE)." >&2
        echo "restore into a new name, verify it, then swap the app over to it." >&2
        exit 1
    fi

    echo "creating database: $TARGET"
    "$PGBIN/createdb" -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -O "$PGUSER" "$TARGET"
    echo "restoring $FILE -> $TARGET"
    "$PGBIN/pg_restore" -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$TARGET" "$FILE"
    echo
    echo "done. $TARGET now holds the restored data. To verify:"
    echo "  $PGBIN/psql -h $PGHOST -p $PGPORT -U $PGUSER -d $TARGET -c '\\dt' | head"
    echo
    echo "To actually cut over once you've checked it:"
    echo "  1. ./honcochat.sh stop"
    echo "  2. rename the live db out of the way, rename $TARGET to $PGDATABASE"
    echo "     (or just point MM_SQLSETTINGS_DATASOURCE at $TARGET permanently)"
    echo "  3. ./honcochat.sh start"
}

case "${1:-backup}" in
backup)  do_backup ;;
list)    do_list ;;
restore) shift; do_restore "$@" ;;
*)       echo "usage: $0 {backup|list|restore <file> [dbname]}" >&2; exit 2 ;;
esac

# --- scheduling -------------------------------------------------------------
# cron (crontab -e), daily at 02:30:
#   30 2 * * * HOME=/home/youruser ROOT=/home/youruser/honco-chat /home/youruser/honco-workspace/chat/backup-honcochat.sh backup >>/home/youruser/honco-chat/logs/backup-cron.log 2>&1
#
# or, if ubuntu3 ever gets a systemd user session, a timer instead:
#   ~/.config/systemd/user/honco-backup.service  (ExecStart=this script backup)
#   ~/.config/systemd/user/honco-backup.timer    (OnCalendar=daily)
#   systemctl --user enable --now honco-backup.timer
