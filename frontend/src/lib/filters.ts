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
 * typeFilters is the certificate type. `layout-grid` is the "no filter
 * applied" glyph, and the four types take the same icons TypeBadge gives
 * them on the rows below.
 */
export const typeFilters: FilterOption[] = [
	{ value: '', label: 'All', icon: 'layout-grid' },
	{ value: 'user', label: 'User', icon: 'user' },
	{ value: 'service', label: 'Service', icon: 'cog' },
	{ value: 'pam', label: 'PAM', icon: 'terminal' },
	{ value: 'console', label: 'Console', icon: 'monitor' }
];

/**
 * statusFilters is whether a certificate still works. The shield and the
 * warning triangle are the glyphs CertRow's validity indicator carries, so
 * a reader filtering on "expired" sees the icon they filtered on.
 */
export const statusFilters: FilterOption[] = [
	{ value: '', label: 'Any', icon: 'layout-grid' },
	{ value: 'live', label: 'Live', icon: 'shield-check' },
	{ value: 'expired', label: 'Expired', icon: 'alert-triangle' }
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
	{ value: 'approved', label: 'Approved', icon: 'check-circle' },
	{ value: 'denied', label: 'Denied', icon: 'x-circle' },
	{ value: '', label: 'Both', icon: 'layout-grid' }
];
