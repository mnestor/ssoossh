<script lang="ts">
  import IconAvatar from '$lib/components/IconAvatar.svelte';
  import type { NotificationProps } from '$lib/types/notification.type';

  // import type { NotificationProps } from './types';

  let { children, src, Icon, when, href, color = 'gray' }: NotificationProps = $props();
</script>

<a {href} class="flex gap-2 border-b px-4 py-3 hover:bg-gray-100 dark:border-gray-600 dark:hover:bg-gray-600">
  <IconAvatar {src} {color}>
    {#if Icon}
      <Icon size="xs" class="text-gray-50 dark:text-white" />
    {/if}
  </IconAvatar>
  <div class="w-full pl-3">
    <div class="mb-1.5 text-sm font-normal text-gray-500 dark:text-gray-300">
      {@render children()}
    </div>
    {#if when}
      <div class="text-primary-700 dark:text-primary-400 text-xs font-medium">{when}</div>
    {/if}
  </div>
</a>
