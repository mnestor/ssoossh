/**
 * isInternalPath reports whether target is a same-origin, path-only URL that
 * is safe to send a browser to.
 *
 * The rule matches the server's own open-redirect guard
 * (server/controller/auth.go's isSafeReturnURL): a leading "/" but not "//",
 * which would be protocol-relative and therefore off-site. Shared by
 * loginURL and by anything rendering a caller-supplied return target, so the
 * two cannot disagree about what the server will accept.
 *
 * This is not the only check standing between a crafted link and an open
 * redirect — the server re-validates whatever it is handed — but agreeing
 * with it here means a bad return_to fails visibly instead of silently
 * landing the user on "/".
 */
export function isInternalPath(target: string | null | undefined): target is string {
	return !!target && target.startsWith('/') && !target.startsWith('//');
}
