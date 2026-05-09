import type { Snippet, Component } from 'svelte';
import type { IndicatorProps } from 'flowbite-svelte';

export interface NotificationProps {
  children: Snippet;
  src: string;
  Icon?: Component;
  when?: string;
  href?: string;
  color: IndicatorProps['color'];
}

export type NotificationData = Omit<NotificationProps, 'children'> & {
  content: string;
};

export interface NotificationListProps {
  notifications: NotificationData[];
}