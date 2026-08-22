import type { BrandingResponse } from '$lib/api/generated/webtypes';

let branding = $state<BrandingResponse | null>(null);

/**
 * load fetches branding config from the unauthenticated /api/branding endpoint.
 * Fails closed — a 404, network error, or any other failure treats it as
 * "no branding configured", so the UI works standalone right now and
 * automatically picks up real data once the backend lands.
 */
export async function loadBranding(): Promise<void> {
	if (branding !== null) {
		return; // Already loaded in this session
	}

	try {
		const response = await fetch('/api/branding');
		if (!response.ok) {
			// 404 or other error: no branding configured, fail closed.
			branding = {};
			return;
		}
		const json = (await response.json()) as { data?: BrandingResponse };
		branding = json.data ?? {};
	} catch {
		// Network error or parse failure: fail closed.
		branding = {};
	}
}

export function getBranding(): BrandingResponse {
	return branding ?? {};
}
