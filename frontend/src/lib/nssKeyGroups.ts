import { listAdminNSSKeys } from '#lib/api/endpoints.js';

// The group names a fleet key's Group field suggests, shared by New key and
// a key's own page. The server takes any group name there (free text,
// matched later against each caller's session groups), so the suggestions
// never limit what can be typed: they are the groups other keys use, from
// the first page of every key, and the reader's own session groups.

/** keyGroupsPageSize is how many keys the suggestions read: the admin list's largest page. */
export const keyGroupsPageSize = 100;

/**
 * mergeGroupSuggestions joins the groups other keys use with the reader's
 * own, each name once, empty names dropped, in sorted order.
 */
export function mergeGroupSuggestions(
	keyGroups: readonly string[],
	ownGroups: readonly string[]
): string[] {
	return [...new Set([...keyGroups, ...ownGroups])].filter((value) => value !== '').sort();
}

/**
 * readKeyGroups reads the groups the first page of every key uses, through
 * the admin key list, which the fleet key admin role may read. It rejects
 * when the read fails; a caller that only wants suggestions ignores that.
 */
export async function readKeyGroups(signal?: AbortSignal): Promise<string[]> {
	const result = await listAdminNSSKeys({ limit: keyGroupsPageSize, signal });
	return result.keys.map((key) => key.group_name);
}
