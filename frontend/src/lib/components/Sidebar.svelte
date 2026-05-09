<script lang="ts">
  import { afterNavigate } from '$app/navigation';
  import { page } from "$app/state";
  import { Sidebar, SidebarDropdownWrapper, SidebarGroup, SidebarItem, SidebarWrapper, SidebarButton, uiHelpers } from 'flowbite-svelte';
  import {
    AngleDownOutline,
    AngleUpOutline,
    CogOutline,
    GithubSolid,
    LockSolid,
    ChartPieOutline
  } from 'flowbite-svelte-icons';

  interface Props {
    drawerHidden: boolean;
  }
  let { drawerHidden = $bindable(false) }: Props = $props();
  const closeDrawer = () => {
    drawerHidden = true;
  };

  
  let activeUrl = $state(page.url.pathname);
  let iconClass = 'flex-shrink-0 w-6 h-6 text-gray-500 transition duration-75 group-hover:text-gray-900 dark:text-gray-300 dark:group-hover:text-white';
  let itemClass = 'flex items-center text-base text-gray-900 transition duration-75 rounded-lg hover:bg-gray-100 group dark:text-gray-200 dark:hover:bg-gray-700 w-full';
  let groupClass = 'pt-2 space-y-2 mb-3';

  const sidebarUi = uiHelpers();
  let isOpen = $state(false);
  const closeSidebar = sidebarUi.close;
  $effect(() => {
    isOpen = sidebarUi.isOpen;
  });

  afterNavigate(() => {
    // this fixes https://github.com/themesberg/flowbite-svelte/issues/364
    document.getElementById('svelte')?.scrollTo({ top: 0 });
    closeDrawer();
    activeUrl = page.url.pathname;
  });

  let posts = [
    { name: 'Dashboard', Icon: ChartPieOutline, href: '/dashboard' },
    { name: 'Logs', Icon: CogOutline, href: '/logs/me' },
    {
      name: 'Admin',
      Icon: LockSolid,
      children: {
        Users: '/admin/users',
        Settings: '/admin/settings'
      }
    }
  ];

  let links = [
    {
      label: 'GitHub Repository',
      href: 'https://github.com/mnestor/ssoossh',
      Icon: GithubSolid
    // },
    // {
    //   label: 'Components',
    //   href: '/components/productdrawer',
    //   Icon: LayersSolid
    // },
    // {
    //   label: 'Support',
    //   href: 'https://github.com/themesberg/flowbite-svelte-admin-dashboard/issues',
    //   Icon: LifeSaverSolid
    }
  ];
</script>

<SidebarButton breakpoint="lg" onclick={sidebarUi.toggle} class="fixed top-[22px] z-40 mb-2" />
<Sidebar
  breakpoint="lg"
  backdrop={false}
  {isOpen}
  {activeUrl}
  {closeSidebar}
  params={{ x: -50, duration: 50 }}
  class="top-0 left-0 mt-[69px] h-screen w-64 bg-gray-50 transition-transform lg:block dark:bg-gray-800"
  classes={{ div: 'h-full px-1 py-1 overflow-y-auto bg-gray-50 dark:bg-gray-800', nonactive: 'p-2', active: 'p-2' }}
>
  <h4 class="sr-only">Main menu</h4>
  <SidebarWrapper class="scrolling-touch h-full max-w-2xs overflow-y-auto bg-white px-3 pt-20 lg:sticky lg:me-0 lg:block lg:h-[calc(100vh-4rem)] lg:pt-5 dark:bg-gray-800">
    <SidebarGroup class={groupClass}>
      {#each posts as { name, Icon, children, href } (name)}
        {#if children}
          <SidebarDropdownWrapper label={name} class="pr-3">
            {#snippet arrowdown()}
              <AngleDownOutline strokeWidth="3.3" size="sm" />
            {/snippet}
            {#snippet arrowup()}
              <AngleUpOutline strokeWidth="3.3" size="sm" />
            {/snippet}
            {#snippet icon()}
              <Icon class={iconClass} />
            {/snippet}
            {#each Object.entries(children) as [title, href]}
              <SidebarItem label={title} {href} spanClass="ml-5" class={itemClass} aClass="w-full" />
            {/each}
          </SidebarDropdownWrapper>
        {:else}
          <SidebarItem label={name} {href} spanClass="ml-3" class={itemClass} aClass="w-full p-0 py-2">
            {#snippet icon()}
              <Icon class={iconClass} />
            {/snippet}
          </SidebarItem>
        {/if}
      {/each}
    </SidebarGroup>

    <SidebarGroup class={groupClass}>
      {#each links as { label, href, Icon } (label)}
        <SidebarItem {label} {href} spanClass="ml-3" class={itemClass}>
          {#snippet icon()}
            <Icon class={iconClass} />
          {/snippet}
        </SidebarItem>
      {/each}
    </SidebarGroup>
  </SidebarWrapper>
</Sidebar>