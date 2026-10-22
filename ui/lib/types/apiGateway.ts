// Wire shapes for the API Gateway route administration endpoints.
export interface APIGatewayRoute {
	id: string;
	uri: string;
	predicates: string[];
	filters: string[];
	virtual_key_ids: string[];
	enabled: boolean;
}

export interface APIGatewayRoutesResponse {
	routes: APIGatewayRoute[];
}

export interface APIGatewayRouteRequest {
	id: string;
	uri: string;
	predicates: string[];
	filters: string[];
	virtual_key_ids: string[];
	enabled: boolean;
}