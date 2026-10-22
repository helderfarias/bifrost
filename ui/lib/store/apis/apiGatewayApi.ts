import type { APIGatewayRoute, APIGatewayRouteRequest, APIGatewayRoutesResponse } from "@/lib/types/apiGateway";
import { baseApi } from "./baseApi";

export const apiGatewayApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getAPIGatewayRoutes: builder.query<APIGatewayRoutesResponse, void>({
			query: () => ({ url: "/api-gateway/routes" }),
			providesTags: ["APIGatewayRoutes"],
		}),
		createAPIGatewayRoute: builder.mutation<APIGatewayRoute, APIGatewayRouteRequest>({
			query: (body) => ({ url: "/api-gateway/routes", method: "POST", body }),
			invalidatesTags: ["APIGatewayRoutes"],
		}),
		updateAPIGatewayRoute: builder.mutation<APIGatewayRoute, { id: string; data: APIGatewayRouteRequest }>({
			query: ({ id, data }) => ({ url: `/api-gateway/routes/${encodeURIComponent(id)}`, method: "PUT", body: data }),
			invalidatesTags: ["APIGatewayRoutes"],
		}),
		deleteAPIGatewayRoute: builder.mutation<void, string>({
			query: (id) => ({ url: `/api-gateway/routes/${encodeURIComponent(id)}`, method: "DELETE" }),
			invalidatesTags: ["APIGatewayRoutes"],
		}),
	}),
});

export const {
	useGetAPIGatewayRoutesQuery,
	useCreateAPIGatewayRouteMutation,
	useUpdateAPIGatewayRouteMutation,
	useDeleteAPIGatewayRouteMutation,
} = apiGatewayApi;