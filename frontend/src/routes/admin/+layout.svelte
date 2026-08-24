<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	const navItems = [
		{ label: 'Users', href: '/admin/users' },
		{ label: 'Certificates', href: '/admin/certificates' },
		{ label: 'Service codes', href: '/admin/service-codes' },
		{ label: 'Config', href: '/admin/config' }
	];
</script>

<div class="flex min-h-screen flex-col">
	<nav class="border-b border-border-subtle bg-surface px-6 py-4">
		<ul class="flex gap-6">
			{#each navItems as item (item.href)}
				<li>
					<a
						href={resolve(item.href)}
						class="inline-block text-sm font-medium {page.url.pathname === item.href
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
