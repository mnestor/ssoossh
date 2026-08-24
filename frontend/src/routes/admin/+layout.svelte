<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	const navItems = [{ route: '/admin/certificates' as const, label: 'Certificates' }] as const;
</script>

<div class="flex min-h-screen flex-col">
	<nav class="border-b border-border-subtle bg-surface px-6 py-4">
		<ul class="flex gap-6">
			{#each navItems as item (item.route)}
				<li>
					<a
						href={resolve(item.route)}
						class="inline-block text-sm font-medium {page.url.pathname === item.route
							? 'border-b-2 border-accent pb-1 text-accent'
							: 'pb-1 text-ink hover:text-accent'}"
					>
						{item.label}
					</a>
				</li>
			{/each}
		</ul>
	</nav>

	<main class="flex flex-1 flex-col items-center px-6 py-8">
		<slot />
	</main>
</div>
