import type { IdentityRole, IdentityRoleInput, IdentityUser, IdentityUserInput, KeycloakProvisionUserInput } from "@/lib/types/identity";
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
		provisionKeycloakUser: builder.mutation<IdentityUser, KeycloakProvisionUserInput>({
			query: (body) => ({ url: "/identity/users/provision/keycloak", method: "POST", body }),
			invalidatesTags: ["IdentityUsers"],
		}),
		updateIdentityUser: builder.mutation<IdentityUser, { id: string; data: IdentityUserInput }>({
			query: ({ id, data }) => ({ url: `/identity/users/${encodeURIComponent(id)}`, method: "PUT", body: data }),
			invalidatesTags: ["IdentityUsers"],
		}),
		replaceIdentityUserVirtualKey: builder.mutation<IdentityUser, { id: string; virtual_key_id: string | null }>({
			query: ({ id, virtual_key_id }) => ({ url: `/identity/users/${encodeURIComponent(id)}/virtual-key`, method: "PUT", body: { virtual_key_id } }),
			invalidatesTags: ["IdentityUsers", "VirtualKeys"],
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
	useProvisionKeycloakUserMutation,
	useUpdateIdentityUserMutation,
	useReplaceIdentityUserVirtualKeyMutation,
	useDeleteIdentityUserMutation,
	useSyncKeycloakUsersMutation,
	useGetIdentityRolesQuery,
	useCreateIdentityRoleMutation,
	useUpdateIdentityRoleMutation,
	useDeleteIdentityRoleMutation,
} = identityApi;
