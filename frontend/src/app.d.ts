// See https://svelte.dev/docs/kit/types#app.d.ts
// for information about these interfaces
declare global {
	namespace App {
		interface Error {
			message: string;
			status?: number;
		}
		// interface Locals {}
		// interface PageData {}
		interface PageState {
			/**
			 * The service account whose codes are listed, carried through
			 * shallow routing. Null means "explicitly closed", which is
			 * distinct from absent: absent falls back to the ?account= search
			 * parameter so a pasted link opens the account it names.
			 *
			 * The only shallow-routed level left. Certificates and service
			 * codes each have a page of their own now, so opening one is an
			 * ordinary navigation and nothing about it lives in page state.
			 */
			accountName?: string | null;
		}
		// interface Platform {}
	}
}

export {};
