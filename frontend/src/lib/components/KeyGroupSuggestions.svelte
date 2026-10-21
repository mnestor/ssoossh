<script lang="ts">
	import { mergeGroupSuggestions, readKeyGroups } from '#lib/nssKeyGroups.js';
	import { session } from '#lib/session.svelte.js';

	// The <datalist> behind a fleet key's Group field, on New key and on a
	// key's own page: the groups other keys use and the reader's own groups
	// (see nssKeyGroups.ts). The other keys' groups are read only for the
	// fleet key admin role, the one role that may create a key or change
	// its group, and only while this list is on the page. A failed read
	// only loses those suggestions; the field still takes any name.

	interface Props {
		/** The datalist's id, which the field's `list` attribute names. */
		id: string;
	}

	let { id }: Props = $props();

	const allowed = $derived(session.user?.is_nss_key_admin ?? false);

	let knownGroups = $state<string[]>([]);
	$effect(() => {
		if (!allowed) return;
		const controller = new AbortController();
		readKeyGroups(controller.signal)
			.then((groups) => {
				knownGroups = groups;
			})
			.catch(() => {});
		return () => controller.abort();
	});

	const suggestions = $derived(mergeGroupSuggestions(knownGroups, session.user?.groups ?? []));
</script>

<datalist {id}>
	{#each suggestions as suggestion (suggestion)}
		<option value={suggestion}></option>
	{/each}
</datalist>
