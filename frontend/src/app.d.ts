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
			 * The certificate whose detail modal is open, carried through
			 * shallow routing. Null means "explicitly closed", which is
			 * distinct from absent: absent falls back to the ?modal= search
			 * parameter so a pasted link opens the certificate it names.
			 */
			modalCertId?: string | null;
		}
		// interface Platform {}
	}
}

export {};
