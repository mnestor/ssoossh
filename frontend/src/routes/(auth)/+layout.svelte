<script lang="ts">
  import modeobserver from '$lib/utils/modeobserver.util';
  import { onMount } from 'svelte';
	// import ConfirmDialog from '$lib/components/confirm-dialog/confirm-dialog.svelte';
	// import Error from '$lib/components/error.svelte';
	// import Header from '$lib/components/header/header.svelte';
	// import { Toaster } from '$lib/components/ui/sonner';
	// import { m } from '$lib/paraglide/messages';
	import { type Snippet } from 'svelte';
	// import '../app.css';
	import type { LayoutData } from '../$types';
  import '../../app.css';
  import Navbar from '$lib/components/Navbar.svelte';
  import Sidebar from '$lib/components/Sidebar.svelte';
  // import type { LayoutProps } from './$types';

	let {
		data,
		children
	}: {
		data: LayoutData;
		children: Snippet;
	} = $props();

	// const { appConfig } = data;
  onMount(modeobserver);

  // const docsRoute = routes.filter((route) => route.path !== '').map((route) => route.path);
  let drawerHidden = $state(false);
</script>

<header class="fixed top-0 z-40 mx-auto w-full flex-none border-b border-gray-200 bg-white dark:border-gray-600 dark:bg-gray-800">
  <Navbar bind:drawerHidden />
</header>
<div class="overflow-hidden lg:flex">
  <Sidebar bind:drawerHidden /> 
  <!-- {docsRoute} /> -->
  <div class="relative h-full w-full overflow-y-auto pt-[70px] lg:ml-64">
    {@render children()}
  </div>
</div>

<!-- {#if !appConfig}
	<Error message="Critical error" showButton={false} />
{:else} -->
	<!-- <Header /> -->
	<!-- {@render children()} -->
<!-- {/if} -->
<!-- <Toaster
	toastOptions={{
		classes: {
			toast: 'border border-primary/30!',
			title: 'text-foreground',
			description: 'text-muted-foreground',
			actionButton: 'bg-primary text-primary-foreground hover:bg-primary/90',
			cancelButton: 'bg-muted text-muted-foreground hover:bg-muted/80',
			closeButton: 'text-muted-foreground hover:text-foreground'
		}
	}}
/>
<ConfirmDialog />
<ModeWatcher disableTransitions={false} /> -->
