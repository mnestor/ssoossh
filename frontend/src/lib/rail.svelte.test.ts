import { beforeEach, describe, expect, it, vi } from 'vitest';

import { rail, RAIL_ADMIN_STORAGE_KEY, RAIL_STORAGE_KEY } from './rail.svelte';

// Test methodology: drive the singleton directly with localStorage stubbed
// per case. What matters is the split between the two pieces of state — the
// collapsed width is a preference that outlives the page, the drawer is a
// transient that must not be — and that a browser refusing site data leaves
// a usable rail rather than a thrown page load.

/** stubStorage installs a working localStorage seeded with `seed`. */
function stubStorage(seed: Record<string, string> = {}) {
	const store = new Map(Object.entries(seed));
	const api = {
		getItem: vi.fn((key: string) => store.get(key) ?? null),
		setItem: vi.fn((key: string, value: string) => void store.set(key, value))
	};
	vi.stubGlobal('localStorage', api);
	return api;
}

/** stubThrowingStorage installs a localStorage that refuses both operations,
 * which is what a browser with site data blocked actually does. */
function stubThrowingStorage() {
	vi.stubGlobal('localStorage', {
		getItem: () => {
			throw new Error('site data blocked');
		},
		setItem: () => {
			throw new Error('site data blocked');
		}
	});
}

beforeEach(() => {
	vi.unstubAllGlobals();
	rail.collapsed = false;
	rail.drawerOpen = false;
	rail.adminOpen = true;
});

describe('rail state', () => {
	it('should start expanded when nothing is stored', () => {
		stubStorage();

		rail.start();

		expect(rail.collapsed).toBe(false);
	});

	it('should start collapsed when that is the stored preference', () => {
		stubStorage({ [RAIL_STORAGE_KEY]: 'true' });

		rail.start();

		expect(rail.collapsed).toBe(true);
	});

	// Anything but the exact string is not a preference for the icon rail.
	it('should start expanded when the stored value is not a boolean', () => {
		stubStorage({ [RAIL_STORAGE_KEY]: 'yes please' });

		rail.start();

		expect(rail.collapsed).toBe(false);
	});

	it('should start expanded when the browser refuses site data', () => {
		stubThrowingStorage();

		rail.start();

		expect(rail.collapsed).toBe(false);
	});

	it('should remember the collapsed preference when it is toggled on', () => {
		const storage = stubStorage();

		rail.toggleCollapsed();

		expect(storage.setItem).toHaveBeenCalledWith(RAIL_STORAGE_KEY, 'true');
	});

	it('should remember the expanded preference when it is toggled back off', () => {
		const storage = stubStorage({ [RAIL_STORAGE_KEY]: 'true' });
		rail.start();

		rail.toggleCollapsed();

		expect(storage.setItem).toHaveBeenLastCalledWith(RAIL_STORAGE_KEY, 'false');
	});

	// The choice still has to take effect on the page in front of the
	// viewer, even when it cannot outlive it.
	it('should still collapse when the preference cannot be stored', () => {
		stubThrowingStorage();

		rail.toggleCollapsed();

		expect(rail.collapsed).toBe(true);
	});

	// The admin group's sections are the point of the group, so a first
	// visit shows them; only an explicit shut is remembered.
	it('should start with the admin group open when nothing is stored', () => {
		stubStorage();

		rail.start();

		expect(rail.adminOpen).toBe(true);
	});

	it('should start with the admin group shut when that is the stored preference', () => {
		stubStorage({ [RAIL_ADMIN_STORAGE_KEY]: 'false' });

		rail.start();

		expect(rail.adminOpen).toBe(false);
	});

	it('should start with the admin group open when the browser refuses site data', () => {
		stubThrowingStorage();

		rail.start();

		expect(rail.adminOpen).toBe(true);
	});

	it('should remember the admin group being shut', () => {
		const storage = stubStorage();

		rail.toggleAdminOpen();

		expect(storage.setItem).toHaveBeenCalledWith(RAIL_ADMIN_STORAGE_KEY, 'false');
	});

	// Two questions, two keys: wanting a narrow rail is not wanting the
	// admin sections hidden.
	it('should not disturb the width preference when the admin group is toggled', () => {
		const storage = stubStorage();

		rail.toggleAdminOpen();

		expect(storage.setItem).not.toHaveBeenCalledWith(RAIL_STORAGE_KEY, expect.anything());
	});

	it('should open the drawer when it is toggled', () => {
		stubStorage();

		rail.toggleDrawer();

		expect(rail.drawerOpen).toBe(true);
	});

	it('should close the drawer on demand', () => {
		stubStorage();
		rail.drawerOpen = true;

		rail.closeDrawer();

		expect(rail.drawerOpen).toBe(false);
	});

	// A phone visitor's open drawer is not a desktop preference, so it must
	// never reach storage.
	it('should not store anything when the drawer opens', () => {
		const storage = stubStorage();

		rail.toggleDrawer();

		expect(storage.setItem).not.toHaveBeenCalled();
	});
});
