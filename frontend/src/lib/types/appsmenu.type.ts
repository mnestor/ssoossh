import type { Component } from 'svelte';

export type MenuItem = {
  name: string;
  href: string;
  icon: Component;
};

export interface AppsMenuProps {
  open?: boolean;
  menu: MenuItem[];
}
