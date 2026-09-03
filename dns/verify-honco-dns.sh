#!/usr/bin/env bash
# Verify honco.in's DNS still matches the pre-migration snapshot (2026-09-02).
#
#   ./verify-honco-dns.sh                        query the public resolvers
#   ./verify-honco-dns.sh ada.ns.cloudflare.com  query Cloudflare directly
#
# Run it against the Cloudflare nameservers BEFORE changing the nameservers at
# GoDaddy. That is the only cheap moment to notice a missing record.
set -uo pipefail

NS="${1:-}"
Q=(dig +short)
[ -n "$NS" ] && Q+=("@$NS")

pass=0; fail=0

check() {  # check <label> <type> <name> <expected-substring>
    local label="$1" type="$2" name="$3" want="$4" got
    got=$("${Q[@]}" "$name" "$type" 2>/dev/null | tr '\n' ' ' | sed 's/[[:space:]]\+/ /g; s/ $//')
    if [ -z "$got" ]; then
        printf '  \033[31mMISSING\033[0m  %-34s %s\n' "$label" "(expected: $want)"
        fail=$((fail+1))
    elif printf '%s' "$got" | grep -qiF -- "$want"; then
        printf '  \033[32mok\033[0m       %-34s %s\n' "$label" "$got"
        pass=$((pass+1))
    else
        printf '  \033[31mCHANGED\033[0m  %-34s got: %s\n' "$label" "$got"
        printf '           %-34s want to contain: %s\n' "" "$want"
        fail=$((fail+1))
    fi
}

echo "honco.in DNS check  ${NS:+(via $NS)}${NS:+ }$([ -z "$NS" ] && echo '(public resolvers)')"
echo

echo "website"
check "A @"                  A     honco.in                    "13.127.206.154"
check "A www"                A     www.honco.in                "13.127.206.154"

echo
echo "mail -- DMARC here is p=reject, so any miss bounces mail"
check "MX primary"           MX    honco.in                    "0 smtp.secureserver.net."
check "MX secondary"         MX    honco.in                    "10 mailstore1.secureserver.net."
check "SPF"                  TXT   honco.in                    "v=spf1 include:secureserver.net -all"
check "DMARC"                TXT   _dmarc.honco.in             "v=DMARC1; p=reject"
check "CNAME email"          CNAME email.honco.in              "email.secureserver.net."

echo
echo "autodiscovery -- Cloudflare's import often drops SRV"
check "SRV _autodiscover"    SRV   _autodiscover._tcp.honco.in "443 autodiscover.secureserver.net."

echo
echo "nameservers (informational)"
printf '  %s\n' "$("${Q[@]}" honco.in NS 2>/dev/null | tr '\n' ' ')"

echo
if [ "$fail" -eq 0 ]; then
    printf '\033[32mall %d records match the pre-migration snapshot\033[0m\n' "$pass"
else
    printf '\033[31m%d record(s) missing or changed, %d ok -- do NOT switch nameservers yet\033[0m\n' "$fail" "$pass"
    exit 1
fi
