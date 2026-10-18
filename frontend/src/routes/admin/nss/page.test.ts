import { describe, expect, it } from 'vitest';

import { load } from './+page.js';

// The NSS directory's own address has no page: its rail entry lands on the
// stored accounts.
describe('NSS directory redirect', () => {
	it('should send /admin/nss to the stored accounts when it is opened', () => {
		expect(() => load()).toThrow(expect.objectContaining({ location: '/admin/nss/accounts' }));
	});
});
