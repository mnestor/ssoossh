/**
 * The filter vocabulary every certificate list shares.
 *
 * `/logs/me` and `/admin/certificates` list the same rows and had grown
 * their own answers to the same three questions — different labels,
 * different icons, different orders, and on the admin side two of the
 * groups on separate lines. Stating them once is what stops a fourth
 * variation appearing with the next list.
 *
 * Each group carries its own "any" option rather than relying on pressing
 * the selected chip to clear it: an explicit chip is discoverable, and a
 * toggle that looks identical to a selection is not.
 */

/** Option is one chip: the value a page filters on, and how it reads. */
export interface FilterOption {
	value: string;
	label: string;
	icon: string;
}

/**
 * The glyph every group's "no filter applied" chip carries. `filter-off`
 * states that condition outright; the destination glyph that used to sit
 * here read as a link to somewhere else, which is the one thing a filter
 * chip must not do.
 */
const ANY = 'filter-off';

/**
 * typeFilters is the certificate type. The four types take the same icons
 * TypeBadge gives them on the rows below.
 */
export const typeFilters: FilterOption[] = [
	{ value: '', label: 'All', icon: ANY },
	{ value: 'user', label: 'User', icon: 'id-badge' },
	{ value: 'service', label: 'Service', icon: 'server-cog' },
	{ value: 'pam', label: 'PAM', icon: 'terminal-2' },
	{ value: 'console', label: 'Console', icon: 'device-desktop' }
];

/**
 * statusFilters is whether a certificate still works. `certificate` and
 * `certificate-off` are the same drawing in two conditions — the glyphs
 * CertRow's validity indicator carries — so a reader filtering on
 * "expired" sees the icon they filtered on, struck through.
 */
export const statusFilters: FilterOption[] = [
	{ value: '', label: 'Any', icon: ANY },
	{ value: 'live', label: 'Live', icon: 'certificate' },
	{ value: 'expired', label: 'Expired', icon: 'certificate-off' }
];

/**
 * outcomeFilters is how a request was decided. The tick and the cross are
 * StatusBadge's own glyphs for an approval and a denial, so a chip is the
 * badge of the rows it selects.
 *
 * Only the caller's own history offers this: a denial issues no
 * certificate, and the admin list reads the certificates table.
 */
export const outcomeFilters: FilterOption[] = [
	{ value: 'approved', label: 'Approved', icon: 'circle-check' },
	{ value: 'denied', label: 'Denied', icon: 'circle-x' },
	{ value: '', label: 'Both', icon: ANY }
];
