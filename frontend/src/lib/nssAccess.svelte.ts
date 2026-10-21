import { listNSSKeys } from '#lib/api/endpoints.js';
import type { CurrentUser } from '#lib/api/types.js';
import { needsKeyCheck } from '#lib/nss.js';

/**
 * NSSAccess answers whether the signed-in viewer holds any fleet key through
 * a key's group, which decides the rail's NSS keys entry for every viewer
 * without the fleet key admin role (see showNSSKeys in #lib/nss).
 *
 * The session does not carry it, so the rail asks /api/nss/keys once per
 * identity wherever NSS is on and the role does not already decide it
 * (needsKeyCheck), and again after a key is created (refresh).
 * A failed read hides the entry: the page is still reachable by its
 * address, and a rail entry that can only show an error is worse than none.
 */
export class NSSAccess {
	/** True once the key list came back non-empty for the current identity. */
	hasKeys = $state(false);

	/** The subject the last check was for, so a re-render does not ask again. */
	private checkedFor: string | null = null;

	/** check asks the server when this viewer's roles leave the entry undecided. */
	check(user: CurrentUser | null): void {
		if (!user || !needsKeyCheck(user)) {
			return;
		}
		if (this.checkedFor === user.subject) {
			return;
		}
		this.checkedFor = user.subject;
		this.hasKeys = false;
		listNSSKeys()
			.then((result) => {
				if (this.checkedFor === user.subject) {
					this.hasKeys = result.keys.length > 0;
				}
			})
			.catch(() => {
				if (this.checkedFor === user.subject) {
					this.hasKeys = false;
				}
			});
	}

	/** refresh asks again for this viewer, after something may have changed the answer. */
	refresh(user: CurrentUser | null): void {
		this.checkedFor = null;
		this.check(user);
	}

	/** clear forgets the answer, for a sign-out or a test. */
	clear(): void {
		this.hasKeys = false;
		this.checkedFor = null;
	}
}

/** The app-wide answer, read by the rail. */
export const nssAccess = new NSSAccess();
