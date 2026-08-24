import type { VersionResponse } from '$lib/api/generated/webtypes';

let version = $state<VersionResponse | null>(null);

/**
 * load fetches the server's build identity from the unauthenticated
 * /api/version endpoint. Fails closed the same way branding does — a 404,
 * network error, or malformed body leaves it unknown, and the footer simply
 * renders nothing rather than guessing at a version or a repository URL.
 */
export async function loadVersion(): Promise<void> {
	if (version !== null) {
		return; // Already loaded in this session
	}

	try {
		const response = await fetch('/api/version');
		if (!response.ok) {
			return;
		}
		const json = (await response.json()) as { data?: VersionResponse };
		version = json.data ?? null;
	} catch {
		// Network error or parse failure: leave it unknown.
	}
}

/** getVersion returns the build identity, or null while it is unknown. */
export function getVersion(): VersionResponse | null {
	return version;
}
