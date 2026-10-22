import type { IdentityRole, IdentityRoleInput, IdentityUser, IdentityUserInput } from "@/lib/types/identity";
import { baseApi } from "./baseApi";

export const identityApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getIdentityUsers: builder.query<{ users: IdentityUser[] }, void>({
			query: () => ({ url: "/identity/users" }),
			providesTags: ["IdentityUsers"],
		}),
		createIdentityUser: builder.mutation<IdentityUser, IdentityUserInput>({
			query: (body) => ({ url: "/identity/users", method: "POST", body }),
			invalidatesTags: ["IdentityUsers"],
		}),
		updateIdentityUser: builder.mutation<IdentityUser, { id: string; data: IdentityUserInput }>({
			query: ({ id, data }) => ({ url: `/identity/users/${encodeURIComponent(id)}`, method: "PUT", body: data }),
			invalidatesTags: ["IdentityUsers"],
		}),
		deleteIdentityUser: builder.mutation<void, string>({
			query: (id) => ({ url: `/identity/users/${encodeURIComponent(id)}`, method: "DELETE" }),
			invalidatesTags: ["IdentityUsers"],
		}),
		syncKeycloakUsers: builder.mutation<{ created: number; updated: number }, void>({
			query: () => ({ url: "/identity/users/sync/keycloak", method: "POST" }),
			invalidatesTags: ["IdentityUsers"],
		}),
		getIdentityRoles: builder.query<{ roles: IdentityRole[] }, void>({
			query: () => ({ url: "/identity/roles" }),
			providesTags: ["IdentityRoles"],
		}),
		createIdentityRole: builder.mutation<IdentityRole, IdentityRoleInput>({
			query: (body) => ({ url: "/identity/roles", method: "POST", body }),
			invalidatesTags: ["IdentityRoles"],
		}),
		updateIdentityRole: builder.mutation<IdentityRole, { id: string; data: IdentityRoleInput }>({
			query: ({ id, data }) => ({ url: `/identity/roles/${encodeURIComponent(id)}`, method: "PUT", body: data }),
			invalidatesTags: ["IdentityRoles"],
		}),
		deleteIdentityRole: builder.mutation<void, string>({
			query: (id) => ({ url: `/identity/roles/${encodeURIComponent(id)}`, method: "DELETE" }),
			invalidatesTags: ["IdentityRoles"],
		}),
	}),
});

export const {
	useGetIdentityUsersQuery,
	useCreateIdentityUserMutation,
	useUpdateIdentityUserMutation,
	useDeleteIdentityUserMutation,
	useSyncKeycloakUsersMutation,
	useGetIdentityRolesQuery,
	useCreateIdentityRoleMutation,
	useUpdateIdentityRoleMutation,
	useDeleteIdentityRoleMutation,
} = identityApi;
