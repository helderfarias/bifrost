export type CircuitBreakerSignal = {
	source: "response_header";
	header_name: string;
	header_value?: string;
	header_contains?: string;
};

export type CircuitBreakerPolicy = {
	name: string;
	enabled: boolean;
	primary_provider: string;
	primary_model: string;
	primary_key_ids?: string[];
	fallback_provider: string;
	fallback_model: string;
	condition: { operator: "OR" | "AND"; signals: CircuitBreakerSignal[] };
	default_cooldown?: string;
	cooldown_header?: string;
};

export type CircuitBreakerPoliciesResponse = { policies: CircuitBreakerPolicy[]; count: number };
export type CircuitBreakerState = { name: string; open: boolean; opened_at?: string; open_until?: string };
export type CircuitBreakerStateResponse = { states: CircuitBreakerState[] };
