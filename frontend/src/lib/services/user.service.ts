import type { ListRequestOptions, Paginated } from '$lib/types/list-request.type';
// import type { UserGroup } from '$lib/types/user-group.type';
import type { User } from '$lib/types/user.type';
import APIService from '$lib/services/api.service';

export default class UserService extends APIService {
	list = async (options?: ListRequestOptions) => {
		const res = await this.api.get('/users', { params: options });
		return res.data as Paginated<User>;
	};

	get = async (id: string) => {
		const res = await this.api.get(`/users/${id}`);
		return res.data as User;
	};

	getCurrent = async () => {
		const res = await this.api.get('/users/me');
		return res.data as User;
	};

	// getUserGroups = async (userId: string) => {
	// 	const res = await this.api.get(`/users/${userId}/groups`);
	// 	return res.data as UserGroup[];
	// };
}
