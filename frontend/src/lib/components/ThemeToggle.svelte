<script lang="ts">
	import { theme, type ThemePreference } from '$lib/theme.svelte';
	import Icon from './Icon.svelte';
	import { railRowClass } from './railClasses';

	// One button that steps through the three states rather than a switch:
	// "follow my system" is a real choice, not the absence of one, so it needs
	// somewhere to live. The icon shows the state, and the label names both
	// the state and what pressing it does — a cycling control that only says
	// where it is leaves a screen reader user guessing where it goes.
	//
	// Two shapes, one behaviour. `icon` is the square button the signed-out
	// header uses; `rail` is a full-width row matching every other row in
	// the rail, because a control that sits in that column and is not shaped
	// like its neighbours reads as something that got left behind.
	interface Props {
		variant?: 'icon' | 'rail';
		/** Only meaningful for the rail variant. */
		collapsed?: boolean;
	}

	let { variant = 'icon', collapsed = false }: Props = $props();

	const icons: Record<ThemePreference, string> = {
		system: 'monitor',
		light: 'sun',
		dark: 'moon'
	};

	const current: Record<ThemePreference, string> = {
		system: 'following your system setting',
		light: 'light',
		dark: 'dark'
	};

	/** The short form, for the rail row where the label is read rather than
	 * announced. The long form stays the accessible name. */
	const shortName: Record<ThemePreference, string> = {
		system: 'System theme',
		light: 'Light theme',
		dark: 'Dark theme'
	};

	const next: Record<ThemePreference, ThemePreference> = {
		system: 'light',
		light: 'dark',
		dark: 'system'
	};

	const label = $derived(
		`Theme: ${current[theme.preference]}. Switch to ${current[next[theme.preference]]}.`
	);
</script>

{#if variant === 'rail'}
	<button
		type="button"
		onclick={() => theme.cycle()}
		aria-label={label}
		title={label}
		class={railRowClass(collapsed)}
	>
		<Icon name={icons[theme.preference]} size="sm" />
		<span class:sr-only={collapsed} class="truncate">{shortName[theme.preference]}</span>
	</button>
{:else}
	<button
		type="button"
		onclick={() => theme.cycle()}
		aria-label={label}
		title={label}
		class="inline-flex h-8 w-8 items-center justify-center rounded-md text-ink-muted transition hover:bg-surface-muted hover:text-ink"
	>
		<Icon name={icons[theme.preference]} size="sm" />
	</button>
{/if}
