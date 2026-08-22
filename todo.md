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
