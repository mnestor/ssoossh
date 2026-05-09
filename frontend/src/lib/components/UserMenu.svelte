<script lang="ts">
  import type { Component, Snippet } from 'svelte';
  import { Avatar, Dropdown, DropdownHeader, DropdownItem } from 'flowbite-svelte';
  import type { UserMenuProps } from '$lib/types/usermenu.type';

  let { name, avatar, menuItems, children, placement = 'bottom-end' }: UserMenuProps = $props();
</script>

<button class="ms-3 rounded-full ring-gray-400 focus:ring-4 dark:ring-gray-600">
  <Avatar size="sm" src={avatar} tabindex={0} />
</button>
<Dropdown {placement} simple>
  <DropdownHeader>
    <span class="block text-sm">{name}</span>
  </DropdownHeader>
  {#each menuItems as menu}
    <DropdownItem>{menu}</DropdownItem>
  {/each}
  {#if children}
    {@render children()}
  {/if}
</Dropdown>