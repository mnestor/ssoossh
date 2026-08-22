* client waiting for approval timeout. need server to kick clients that have been waiting for X amount of time, default 5 minutes.
* Revisit: web UI session TTL vs certificate lifetime are two different
  things and must not be tuned as if they were one knob.
  - Session TTL (`http.cookie_max_age`, `defaultCookieMaxAge` in
    `server/bootstrap/router.go`) bounds the browser login session, and
    feat/auth-roles made it double as the admin-role revocation window.
    It was dropped from 12h to a 15m default.
  - Certificate lifetime is configured per cert type and governs how long
    an already-issued certificate stays valid. Shortening the session does
    not shorten a live certificate, and lengthening it does not extend one.
  - Open question: 15m was chosen as an admin revocation bound but currently
    applies to every user's web session. Decide whether these should split
    into separate settings, and confirm the authoritative NASA-defined value
    (human task, not yet done).

* Coverage exclusions: replace hand-maintained line ranges with markers.
  - `exclude-from-coverage.txt` entries are position-based regexes. After the
    13-branch merge, 39 of them in actively-compiled code matched nothing:
    17 in server/service/certrequest.go, 6 in server/service/auth.go, 5 in
    server/controller/auth.go, the rest scattered. They had silently stopped
    excluding.
  - Do NOT simply regenerate the ranges. The whole mechanism is worth 0.8
    points (87.6% unfiltered vs 88.4% filtered, 28 working patterns), and
    regenerating hides ~39 more blocks without adding a test. CI does not
    apply the exclusions at all (codecover.yaml uploads raw coverage.txt),
    so the unfiltered number is the one anyone outside this repo sees.
  - Plan, in order: (1) a `//coverage:ignore <reason>` marker plus a
    generator that emits exclude-from-coverage.txt from the AST during
    `make cover`, so ranges cannot drift and the justification sits next to
    the code; (2) treat the 39 dead exclusions as a worklist of untested
    code and write tests where the path is now reachable, since much of
    certrequest.go was rewritten after those exclusions were written.
  - Assigned to a Fable-driven test pass once the current round lands.
  - Note: 21 further patterns (pam_ssoossh.go, pam.go, conversation.go,
    test/e2e) are cgo or build-tagged and contribute no blocks to these
    profiles at all. Confirm whether `make test-pam` produces a profile they
    ever apply to; if not, they should say so or be removed.
