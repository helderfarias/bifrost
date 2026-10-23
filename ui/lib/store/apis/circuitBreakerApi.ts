import type { CircuitBreakerPoliciesResponse, CircuitBreakerPolicy, CircuitBreakerStateResponse } from "@/lib/types/circuitBreaker";
import { baseApi } from "./baseApi";

export const circuitBreakerApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getCircuitBreakerPolicies: builder.query<CircuitBreakerPoliciesResponse, void>({ query: () => ({ url: "/circuit-breaker/policies" }), providesTags: ["CircuitBreakerPolicies"] }),
		createCircuitBreakerPolicy: builder.mutation<CircuitBreakerPolicy, CircuitBreakerPolicy>({ query: (body) => ({ url: "/circuit-breaker/policies", method: "POST", body }), invalidatesTags: ["CircuitBreakerPolicies", "CircuitBreakerState"] }),
		updateCircuitBreakerPolicy: builder.mutation<CircuitBreakerPolicy, CircuitBreakerPolicy>({ query: (body) => ({ url: `/circuit-breaker/policies/${encodeURIComponent(body.name)}`, method: "PUT", body }), invalidatesTags: ["CircuitBreakerPolicies", "CircuitBreakerState"] }),
		deleteCircuitBreakerPolicy: builder.mutation<void, string>({ query: (name) => ({ url: `/circuit-breaker/policies/${encodeURIComponent(name)}`, method: "DELETE" }), invalidatesTags: ["CircuitBreakerPolicies", "CircuitBreakerState"] }),
		getCircuitBreakerState: builder.query<CircuitBreakerStateResponse, void>({ query: () => ({ url: "/circuit-breaker/state" }), providesTags: ["CircuitBreakerState"] }),
	}),
});

export const { useGetCircuitBreakerPoliciesQuery, useCreateCircuitBreakerPolicyMutation, useUpdateCircuitBreakerPolicyMutation, useDeleteCircuitBreakerPolicyMutation, useGetCircuitBreakerStateQuery } = circuitBreakerApi;
