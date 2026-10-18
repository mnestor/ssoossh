import { redirect } from '@sveltejs/kit';
import { resolve } from '$app/paths';

// The NSS directory has no page of its own: its rail entry opens the stored
// accounts, the list a reader most often comes for ("host X cannot resolve
// bob"). The groups are one press away in that page's heading.
export function load() {
	redirect(307, resolve('/admin/nss/accounts'));
}
