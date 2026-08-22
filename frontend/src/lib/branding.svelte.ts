/**
 * Branding configuration fetched at app startup from an unauthenticated
 * API endpoint. When the backend endpoint lands, this should be reconciled
 * with the real webtypes.BrandingResponse type.
 *
 * All fields are optional: if the endpoint does not exist or returns empty
 * values, the UI renders without branding, matching the "off unless deployed
 * explicitly" requirement.
 */
interface BrandingConfig {
	org_name?: string;
	logo_url?: string;
	login_notice?: string;
}

let branding = $state<BrandingConfig | null>(null);

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
		const json = (await response.json()) as { data?: BrandingConfig };
		branding = json.data ?? {};
	} catch {
		// Network error or parse failure: fail closed.
		branding = {};
	}
}

export function getBranding(): BrandingConfig {
	return branding ?? {};
}
