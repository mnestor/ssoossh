import { redirect } from '@sveltejs/kit';

// There is no content at the root. Signed-in users want the dashboard; the
// dashboard bounces to /login itself when there is no session, so this does
// not need to know which case it is in.
export function load() {
	redirect(307, '/dashboard');
}
