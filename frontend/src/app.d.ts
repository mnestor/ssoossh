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

			/**
			 * The service enrollment whose detail modal is open, on the same
			 * terms as modalCertId. A separate key rather than a shared one
			 * because the two lists hold different things: a shared key would
			 * let a ?modal= id from one page resolve against the other.
			 */
			modalEnrollmentId?: string | null;
		}
		// interface Platform {}
	}
}

export {};
