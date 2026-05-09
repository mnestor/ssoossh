import type { DropdownProps } from 'flowbite-svelte';
import type { Snippet } from 'svelte';


export interface UserMenuProps {
  name: string;
  avatar: string;
  menuItems: string[];
  children?: Snippet;
  placement?: DropdownProps['placement'];
}