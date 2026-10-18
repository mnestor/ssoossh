<script lang="ts">
	import { resolve } from '$app/paths';

	import { session } from '#lib/session.svelte.js';

	// One sentence pointing at a section of the Directory page from the NSS
	// viewer. The Directory is admin only (issue #136) while the viewer is
	// for auditors and above, so an admin gets the section as a link and
	// anyone else gets the same sentence naming who can act, with nothing to
	// follow into an access-denied card.
	interface Props {
		/** The Directory page section meant. */
		section: 'probe' | 'sync' | 'pending' | 'collisions';
		testid?: string;
	}

	let { section, testid }: Props = $props();

	// What the reader would do there, the section's name, and its fragment
	// on the Directory page (the PageSection ids).
	const targets = {
		probe: {
			does: 'check what the directory itself says with',
			label: 'Probe',
			anchor: 'probe'
		},
		sync: { does: 'run a sync now from', label: 'Sync section', anchor: 'sync' },
		pending: {
			does: 'accept the new number in',
			label: 'Pending id changes panel',
			anchor: 'pending-id-changes'
		},
		collisions: {
			does: 'see both contending entries in',
			label: 'Collisions panel',
			anchor: 'collisions'
		}
	} as const;

	const target = $derived(targets[section]);
	const admin = $derived(session.user?.is_admin ?? false);
	const capitalised = $derived(target.does.charAt(0).toUpperCase() + target.does.slice(1));
</script>

{#if admin}<span data-testid={testid}
		>{capitalised} the
		<a
			href="{resolve('/admin/directory')}#{target.anchor}"
			class="text-accent hover:underline"
			data-testid={testid ? `${testid}-link` : undefined}>{target.label}</a
		> on the Directory page.</span
	>{:else}<span data-testid={testid}
		>An admin can {target.does} the {target.label} on the Directory page.</span
	>{/if}
