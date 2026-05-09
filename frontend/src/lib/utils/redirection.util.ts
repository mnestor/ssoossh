import type { User } from '$lib/types/user.type';

// Returns the path to redirect to based on the current path and user authentication status
// If no redirect is needed, it returns null
export function getAuthRedirectPath(path: string, user: User | null) {
	const isSignedIn = !!user;
	const isAdmin = user?.isAdmin;
  // return;

	const isUnauthenticatedOnlyPath = ['/login', '/'].includes(path);
  //  path == '/login' || path == '/';
	const isPublicPath = ['/health', '/healthz'].includes(path);
	const isAdminPath = path.startsWith('/admin/');

	if (!isUnauthenticatedOnlyPath && !isPublicPath && !isSignedIn) {
		return '/oauth/login?next=' + btoa("/dashboard").replaceAll("=","");
	}

	if (isUnauthenticatedOnlyPath && isSignedIn) {
		return '/dashboard';
	}

	if (isAdminPath && !isAdmin) {
		return '/dashboard';
	}
}
