#!/usr/bin/env python3
"""Honco Chat: the vendor wordmark and edition badge in the web client.

debrand-polish.py handles translated strings and debrand-webapp.py the static
shell. What neither reaches is the React components that draw the vendor's
logo SVG on the login page and in the global header, the "TEAM EDITION" badge
next to it, and the "(c) <year> Mattermost Inc." footers on the login/signup
routes and on the older not-logged-in template (password reset). Those are code, not strings, so they get a small component swap here.

Adds one component (honco_logo.tsx: a mark plus the site name, drawn with
currentColor so it follows whatever surface it sits on) and points the three
call sites at it. Package paths (@mattermost/*, mattermost-redux) are never
touched. Idempotent: every edit checks for its own marker first.

Rebuild the client afterwards (see DEBRAND.md, "The web client").
"""
import os
import re
import sys

W = os.path.expanduser("~/honco-chat/server/webapp/channels/src")
BRAND = "Honco Chat"
changed = 0


def patch(rel, fn):
    global changed
    p = os.path.join(W, rel)
    if not os.path.isfile(p):
        print("missing:", rel)
        sys.exit(1)
    body = open(p, encoding="utf-8").read()
    new = fn(body)
    if new != body:
        open(p, "w", encoding="utf-8").write(new)
        changed += 1
        print("patched", rel)
    else:
        print("already ok", rel)


# --- 1. the wordmark ----------------------------------------------------------

LOGO = """// Honco Chat wordmark. Drawn in currentColor so the same component works on
// the light login page and the dark global header; the caller sets the colour.

import React from 'react';

type Props = {
    width?: number;
    height?: number;
    className?: string;
    color?: string;
};

const HoncoLogo = (props: Props) => {
    const height = props.height || 24;
    return (
        <span
            className={props.className}
            style={{display: 'inline-flex', alignItems: 'center', gap: Math.round(height / 3), lineHeight: 1, color: props.color}}
        >
            <svg
                width={height}
                height={height}
                // Inline so a host stylesheet's `svg { width: ... }` (the
                // login header has one) cannot stretch the mark.
                style={{width: height, height, flexShrink: 0}}
                viewBox='0 0 24 24'
                fill='none'
                xmlns='http://www.w3.org/2000/svg'
                aria-hidden='true'
            >
                <rect
                    x='1'
                    y='1'
                    width='22'
                    height='22'
                    rx='6'
                    style={{fill: 'currentColor'}}
                />
                <path
                    d='M7 6.5h2.6v4.4h4.8V6.5H17v11h-2.6v-4.3H9.6v4.3H7z'
                    style={{fill: '#fff'}}
                />
            </svg>
            <span
                style={{
                    fontFamily: 'Metropolis, "Open Sans", sans-serif',
                    fontWeight: 700,
                    fontSize: Math.round(height * 0.8),
                    letterSpacing: '-0.01em',
                    whiteSpace: 'nowrap',
                }}
            >
                {'%s'}
            </span>
        </span>
    );
};

export default HoncoLogo;
""" % BRAND

logo_path = os.path.join(W, "components/common/svg_images_components/honco_logo.tsx")
if not os.path.isfile(logo_path) or open(logo_path, encoding="utf-8").read() != LOGO:
    open(logo_path, "w", encoding="utf-8").write(LOGO)
    changed += 1
    print("wrote components/common/svg_images_components/honco_logo.tsx")
else:
    print("already ok honco_logo.tsx")


# --- 2. login / signup header -----------------------------------------------

def header(s):
    s = s.replace(
        "import Logo from 'components/common/svg_images_components/logo_dark_blue_svg';",
        "import Logo from 'components/common/svg_images_components/honco_logo';",
    )
    # No edition badge: the product is Honco Chat, not an edition of something.
    wordmark = "<Logo height={28} color='var(--center-channel-color)'/>"
    s = s.replace(
        "freeBanner = <><Logo/><span className='freeBadge'>{'TEAM EDITION'}</span></>;",
        "freeBanner = %s;" % wordmark,
    )
    s = s.replace(
        "freeBanner = <><Logo/><span className='freeBadge'>{'ENTRY EDITION'}</span></>;",
        "freeBanner = %s;" % wordmark,
    )
    # an earlier revision of this script placed the wordmark without a colour
    s = s.replace("freeBanner = <Logo height={28}/>;", "freeBanner = %s;" % wordmark)
    s = s.replace("title = <Logo height={28}/>;", "title = %s;" % wordmark)
    # The wordmark already says the site name; do not print it twice.
    s = s.replace(
        "    if (title === 'Mattermost') {\n        if (freeBanner) {",
        "    if (title === 'Mattermost' || title === '%s') {\n        if (freeBanner) {" % BRAND,
    )
    s = s.replace("title = <Logo/>;", "title = %s;" % wordmark)
    s = s.replace("const ariaLabel = SiteName || 'Mattermost';", "const ariaLabel = SiteName || '%s';" % BRAND)
    return s


patch("components/header_footer_route/header.tsx", header)


# --- 3. global header product branding --------------------------------------

def product_branding(s):
    s = s.replace(
        "import Logo from 'components/common/svg_images_components/logo_dark_blue_svg';",
        "import Logo from 'components/common/svg_images_components/honco_logo';",
    )
    s = s.replace(
        """const StyledLogo = styled(Logo)`
    path {
        fill: rgba(var(--sidebar-text-rgb), 0.75);
    }
`;""",
        """const StyledLogo = styled(Logo)`
    color: rgba(var(--sidebar-text-rgb), 0.9);
`;""",
    )
    # The badge stays defined (other code may import nothing from here, but
    # keeping the symbol avoids an unused-variable lint failure) and is
    # simply no longer rendered.
    s = s.replace(
        """            <StyledLogo
                width={116}
                height={20}
            />
            <Badge>{badgeText}</Badge>""",
        """            <StyledLogo
                width={116}
                height={20}
            />
            {false && <Badge>{badgeText}</Badge>}""",
    )
    return s


patch(
    "components/global_header/left_controls/product_menu/product_branding_team_edition/product_branding_free_edition.tsx",
    product_branding,
)


# --- 4. footer copyright -----------------------------------------------------

def footer(s):
    return s.replace(
        "{`© ${new Date().getFullYear()} Mattermost Inc.`}",
        "{`© ${new Date().getFullYear()} Honco`}",
    )


patch("components/header_footer_route/footer.tsx", footer)


# --- 4b. the older not-logged-in template (password reset, signup completion,
#         terms) has its own footer with the vendor name written in twice ----

def template_footer(s):
    s = s.replace("<span\n                            id='company_name'\n                            className='pull-right footer-site-name'\n                        >\n                            {'Mattermost'}",
                  "<span\n                            id='company_name'\n                            className='pull-right footer-site-name'\n                        >\n                            {'%s'}" % BRAND)
    s = s.replace(
        "{`© 2015-${new Date().getFullYear()} Mattermost, Inc.`}",
        "{`© ${new Date().getFullYear()} Honco`}",
    )
    return s


patch("components/header_footer_template/header_footer_template.tsx", template_footer)

# --- 4c. the About dialog (product menu > About): vendor logo and name -------

def about_modal(s):
    s = s.replace(
        "import MattermostLogo from 'components/widgets/icons/mattermost_logo';",
        "import HoncoLogo from 'components/common/svg_images_components/honco_logo';",
    )
    s = s.replace("<MattermostLogo/>", "<HoncoLogo height={36}/>")
    s = s.replace("{'Mattermost'} {title}", "{'%s'} {title}" % BRAND)
    # "Join the community at mattermost.com/community/": a vendor link with
    # no Honco equivalent. The translated prefix is already Honco's; the
    # hard-coded URL text is not, so the line goes.
    s = s.replace("            >
                {'mattermost.com/community/'}
            </ExternalLink>",
                  "            >
                {'chat.honco.in/help'}
            </ExternalLink>")
    return s


patch("components/about_build_modal/about_build_modal.tsx", about_modal)


# --- 5. the vendor logo's alt text, wherever a translated string carries it --
en = os.path.join(W, "i18n/en.json")
if os.path.isfile(en):
    body = open(en, encoding="utf-8").read()
    new = body.replace('"Mattermost Logo"', '"%s logo"' % BRAND)
    if new != body:
        open(en, "w", encoding="utf-8").write(new)
        changed += 1
        print("patched i18n/en.json (logo alt text)")
    else:
        print("already ok i18n/en.json")

print("files changed:", changed)
