#!/usr/bin/env python3
"""Honco Chat: the Profile Photo section in Profile Settings.

Mattermost already has everything a profile photo needs -- the image is
stored by the server's own profile-image API (POST/DELETE
/api/v4/users/{id}/image, permission-checked against the session), served at
/api/v4/users/{id}/image?_={last_picture_update} so a change replaces it
everywhere at once, and edited in Profile Settings through the stock
SettingPicture component (preview, select, save, remove, cancel, with type
and size validation on both client and server). Nothing here adds storage,
tables, endpoints or a second avatar.

What the stock section lacks is the wording people expect ("Profile
Picture" / "Select" / "Save"), any acknowledgement after a successful
upload, and a plain message when the server refuses. This script:

  * renames the section and its controls (Profile Photo, Change photo,
    Save photo, Remove photo) -- the team-icon dialog, which shares the
    component, keeps its own labels;
  * shows "Profile photo updated." / "Profile photo removed." in the
    section's summary line after a successful save, without a reload;
  * replaces raw server error text with "Unable to update profile photo.
    Please try again." (the client-side type and size checks keep their
    specific messages, which are useful).

Idempotent: every edit checks for its own marker first. Rebuild the web
client afterwards (see DEBRAND.md, "The web client").
"""
import os
import re
import sys

W = os.path.expanduser("~/honco-chat/server/webapp/channels/src")
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


def must(body, needle, rel):
    if needle not in body:
        print("expected text not found in %s:\n  %s" % (rel, needle.strip().splitlines()[0]))
        sys.exit(1)


# --- 1. the strings ----------------------------------------------------------

REPLACE = {
    "user.settings.general.profilePicture": "Profile Photo",
    "user.settings.general.uploadImage": "Click 'Edit' to change your photo.",
    "user.settings.general.mobile.uploadImage": "Click to change your photo",
    "user.settings.general.imageUpdated": "Photo last updated {date}",
    "user.settings.general.validImage": "Only JPG, PNG or BMP images can be used as a profile photo.",
    "user.settings.general.imageTooLarge": "Unable to update profile photo. The file is larger than the maximum allowed size.",
    "setting_picture.help.profile": "JPG, PNG or BMP \u2022 maximum size {max}",
    "setting_picture.remove_profile_picture": "Remove photo",
}
ADD = {
    "setting_picture.select.profile": "Change photo",
    "setting_picture.save.profile": "Save photo",
    "user.settings.general.photoUpdated": "Profile photo updated.",
    "user.settings.general.photoRemoved": "Profile photo removed.",
    "user.settings.general.photoFailed": "Unable to update profile photo. Please try again.",
}


def strings(body):
    for key, val in REPLACE.items():
        pat = re.compile(r'^(  "%s": )"(.*)",?$' % re.escape(key), re.M)
        m = pat.search(body)
        if not m:
            print("missing translation key", key)
            sys.exit(1)
        line = m.group(0)
        new = line[: line.index('"', len(m.group(1)))] + '"' + val.replace('"', '\\"') + '"' + ("," if line.endswith(",") else "")
        body = body.replace(line, new)
    for key, val in ADD.items():
        if '"%s":' % key in body:
            continue
        # Keep the file sorted the way upstream keeps it: put each new key
        # right after the key it belongs beside.
        anchor = {
            "setting_picture.select.profile": '"setting_picture.select": ',
            "setting_picture.save.profile": '"setting_picture.save": ',
            "user.settings.general.photoUpdated": '"user.settings.general.nickname": ',
            "user.settings.general.photoRemoved": '"user.settings.general.nickname": ',
            "user.settings.general.photoFailed": '"user.settings.general.nickname": ',
        }[key]
        m = re.search(r'^  %s.*$' % re.escape(anchor), body, re.M)
        if not m:
            print("anchor not found for", key)
            sys.exit(1)
        body = body[: m.end()] + '\n  "%s": "%s",' % (key, val) + body[m.end():]
    return body


patch("i18n/en.json", strings)


# --- 2. the shared picture component: profile-specific labels --------------

PIC = "components/setting_picture.tsx"


def picture(body):
    if "Honco: the profile photo" in body:
        return body
    must(body, "        const img = this.renderImg();\n", PIC)
    body = body.replace(
        "        const img = this.renderImg();\n",
        "        const img = this.renderImg();\n\n"
        "        // Honco: the profile photo gets its own labels; the team icon,\n"
        "        // which shares this component, keeps the stock ones.\n"
        "        const isProfile = this.props.imageContext === 'profile';\n"
        "        const selectMsg = isProfile ? {id: 'setting_picture.select.profile', defaultMessage: 'Change photo'} : {id: 'setting_picture.select', defaultMessage: 'Select'};\n"
        "        const saveMsg = isProfile ? {id: 'setting_picture.save.profile', defaultMessage: 'Save photo'} : {id: 'setting_picture.save', defaultMessage: 'Save'};\n",
        1,
    )
    pairs = [
        ("aria-label={localizeMessage({id: 'setting_picture.select', defaultMessage: 'Select'})}",
         "aria-label={localizeMessage(selectMsg)}"),
        ("                        <FormattedMessage\n"
         "                            id='setting_picture.select'\n"
         "                            defaultMessage='Select'\n"
         "                        />",
         "                        <FormattedMessage {...selectMsg}/>"),
        ("localizeMessage({id: 'setting_picture.save', defaultMessage: 'Save'})",
         "localizeMessage(saveMsg)"),
        ("                            <FormattedMessage\n"
         "                                id='setting_picture.save'\n"
         "                                defaultMessage='Save'\n"
         "                            />",
         "                            <FormattedMessage {...saveMsg}/>"),
    ]
    for old, new in pairs:
        must(body, old, PIC)
        body = body.replace(old, new, 1)
    return body


patch(PIC, picture)


# --- 3. profile settings: success state, plain failure message -------------

GEN = "components/user_settings/general/user_settings_general.tsx"


def general(body):
    if "photoUpdated" in body:
        return body

    # message holders
    must(body, "    validImage: {\n", GEN)
    body = body.replace(
        "    validImage: {\n",
        "    photoUpdated: {\n"
        "        id: 'user.settings.general.photoUpdated',\n"
        "        defaultMessage: 'Profile photo updated.',\n"
        "    },\n"
        "    photoRemoved: {\n"
        "        id: 'user.settings.general.photoRemoved',\n"
        "        defaultMessage: 'Profile photo removed.',\n"
        "    },\n"
        "    photoFailed: {\n"
        "        id: 'user.settings.general.photoFailed',\n"
        "        defaultMessage: 'Unable to update profile photo. Please try again.',\n"
        "    },\n"
        "    validImage: {\n",
        1,
    )

    # state
    must(body, "    pictureError?: string | null;\n", GEN)
    body = body.replace(
        "    pictureError?: string | null;\n",
        "    pictureError?: string | null;\n"
        "    pictureNotice?: string;\n",
        1,
    )
    must(body, "            pictureFile: null,\n", GEN)
    body = body.replace(
        "            pictureFile: null,\n",
        "            pictureFile: null,\n"
        "            pictureNotice: '',\n",
        1,
    )

    # upload: success notice, plain failure
    old = (
        "                if (data) {\n"
        "                    this.updateSection('');\n"
        "                    this.submitActive = false;\n"
        "                } else if (err) {\n"
        "                    const state = this.setupInitialState(this.props);\n"
        "                    state.serverError = err.message;\n"
        "                    this.setState(state);\n"
        "                }"
    )
    must(body, old, GEN)
    body = body.replace(
        old,
        "                if (data) {\n"
        "                    this.updateSection('');\n"
        "                    this.submitActive = false;\n"
        "                    // Honco: the section closes; say what happened in its summary line.\n"
        "                    this.setState({pictureNotice: formatMessage(holders.photoUpdated)});\n"
        "                } else if (err) {\n"
        "                    const state = this.setupInitialState(this.props);\n"
        "                    // Honco: a plain message rather than the server's own text.\n"
        "                    state.serverError = formatMessage(holders.photoFailed);\n"
        "                    this.setState(state);\n"
        "                }",
        1,
    )

    # remove: success notice, plain failure
    old = (
        "            await this.props.actions.setDefaultProfileImage(this.props.user.id);\n"
        "            this.updateSection('');\n"
        "            this.submitActive = false;\n"
        "        } catch (err) {\n"
        "            let serverError;\n"
        "            if (err.message) {\n"
        "                serverError = err.message;\n"
        "            } else {\n"
        "                serverError = err;\n"
        "            }\n"
        "            this.setState({serverError, emailError: '', pictureError: '', sectionIsSaving: false});"
    )
    must(body, old, GEN)
    body = body.replace(
        old,
        "            await this.props.actions.setDefaultProfileImage(this.props.user.id);\n"
        "            this.updateSection('');\n"
        "            this.submitActive = false;\n"
        "            this.setState({pictureNotice: this.props.intl.formatMessage(holders.photoRemoved)});\n"
        "        } catch (err) {\n"
        "            // Honco: a plain message rather than the server's own text.\n"
        "            const serverError = this.props.intl.formatMessage(holders.photoFailed);\n"
        "            this.setState({serverError, emailError: '', pictureError: '', sectionIsSaving: false});",
        1,
    )

    # summary line under the collapsed section
    old = (
        "        return (\n"
        "            <>\n"
        "                <SettingItem\n"
        "                    active={active}\n"
        "                    areAllSectionsInactive={this.props.activeSection === ''}\n"
        "                    title={formatMessage(holders.profilePicture)}\n"
    )
    must(body, old, GEN)
    body = body.replace(
        old,
        "        if (this.state.pictureNotice) {\n"
        "            minMessage = this.state.pictureNotice;\n"
        "        }\n" + old,
        1,
    )
    return body


patch(GEN, general)


# --- 4. choosing another file after a failure clears the old message -------


def fresh_choice(body):
    if "a fresh choice clears" in body:
        return body
    old = (
        "            this.submitActive = true;\n"
        "            this.setState({pictureError: null});\n"
    )
    must(body, old, GEN)
    return body.replace(
        old,
        "            this.submitActive = true;\n"
        "            // Honco: a fresh choice clears the previous attempt's message too.\n"
        "            this.setState({pictureError: null, serverError: ''});\n",
        1,
    )


patch(GEN, fresh_choice)

print("done; %d file(s) changed" % changed)
