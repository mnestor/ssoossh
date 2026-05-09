import type { Locale } from '$lib/paraglide/runtime';
// import type { UserGroup } from './user-group.type';

export type User = {
	id: string;
	username: string;
	email: string | undefined;
	firstName: string;
	lastName?: string;
	displayName: string;
	isAdmin: boolean;
	// userGroups: UserGroup[];
	locale?: Locale;
	ldapId?: string;
	disabled?: boolean;
};

