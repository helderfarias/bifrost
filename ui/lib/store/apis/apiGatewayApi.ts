import { baseApi } from "./baseApi";

export interface APIConsumer {
	name: string;
	token_sha256: string;
	capabilities: string[];
}
export interface APIRoute {
	id: string;
	name: string;
	upstream: string;
	path: string;
	strip_prefix: boolean;
	relay_authorization: boolean;
	enabled: boolean;
	capabilities: string[];
	consumers: APIConsumer[];
	requests_per_minute: number;
	timeout_seconds: number;
	failure_threshold: number;
	recovery_seconds: number;
}
export interface APIRoutesResponse {
	routes: APIRoute[];
	revision: string;
}
const apiGatewayApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getAPIRoutes: builder.query<APIRoutesResponse, void>({ query: () => "/api-gateway/routes" }),
		saveAPIRoutes: builder.mutation<APIRoutesResponse, APIRoutesResponse>({
			query: (body) => ({ url: "/api-gateway/routes", method: "PUT", body }),
			async onQueryStarted(_, { dispatch, queryFulfilled }) {
				try {
					const { data } = await queryFulfilled;
					dispatch(apiGatewayApi.util.upsertQueryData("getAPIRoutes", undefined, data));
				} catch {
					/* The form displays mutation errors. */
				}
			},
		}),
	}),
});
export const { useGetAPIRoutesQuery, useSaveAPIRoutesMutation } = apiGatewayApi;