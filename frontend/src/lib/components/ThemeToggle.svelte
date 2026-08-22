<script lang="ts">
	import { theme, type ThemePreference } from '$lib/theme.svelte';
	import Icon from './Icon.svelte';

	// One button that steps through the three states rather than a switch:
	// "follow my system" is a real choice, not the absence of one, so it needs
	// somewhere to live. The icon shows the state, and the label names both
	// the state and what pressing it does — a cycling control that only says
	// where it is leaves a screen reader user guessing where it goes.
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

	const next: Record<ThemePreference, ThemePreference> = {
		system: 'light',
		light: 'dark',
		dark: 'system'
	};

	const label = $derived(
		`Theme: ${current[theme.preference]}. Switch to ${current[next[theme.preference]]}.`
	);
</script>

<button
	type="button"
	onclick={() => theme.cycle()}
	aria-label={label}
	title={label}
	class="inline-flex h-8 w-8 items-center justify-center rounded-md text-ink-muted transition hover:bg-surface-muted hover:text-ink"
>
	<Icon name={icons[theme.preference]} size="sm" />
</button>
