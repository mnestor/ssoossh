<script lang="ts">
  import UserMenu from '$lib/components/UserMenu.svelte';
  // import type { UserAccount } from '$lib/types/usermenu.type';
	import userStore from '$lib/stores/user.store';

  import { DarkMode, DropdownItem, NavBrand, Navbar, DropdownDivider } from 'flowbite-svelte';
  import {
    CogOutline,
    UsersGroupSolid,
    CameraPhotoOutline
  } from 'flowbite-svelte-icons';
  import '../../app.css';
  import type { NotificationProps } from '$lib/types/notification.type';

  type NotificationData = Omit<NotificationProps, 'children'> & {
    content: string;
  };

  interface Props {
    drawerHidden?: boolean;
  }

  let { drawerHidden = $bindable(false) }: Props = $props();

  const menu = [
    { name: 'Settings', href: '/settings', icon: CogOutline },
  ];
  const menuItems = ['Settings'];

  // for avatar
  // const users = mapUsersWithAvatars(Users);
  const user: {
    name: string;
    avatar: string;
  } = {
    avatar: "",
    name: "mike"
  };
</script>

<Navbar class="mx-10 sm:mx-0">
  <NavBrand href="/" class="mx-10">
    <!-- <img src="/images/flowbite-svelte-icon-logo.svg" class="me-2.5 h-6 sm:h-8" alt="Flowbite Logo" /> -->
    <span class="ml-px self-center text-xl font-semibold whitespace-nowrap sm:text-2xl dark:text-white"> ssoossh </span>
  </NavBrand>
  <div class="ms-auto flex items-center text-gray-500 sm:order-2 dark:text-gray-300 h-[49px]">
    <DarkMode />
    <UserMenu name={userStore?.username} avatar={user.avatar} {menuItems}>
      <DropdownDivider />
      <DropdownItem>Sign out</DropdownItem>
    </UserMenu>
  </div>
</Navbar>