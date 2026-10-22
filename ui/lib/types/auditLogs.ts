export type AuditAction = "create" | "update" | "delete" | "authenticate" | "authorize" | "export";
export type AuditOutcome = "success" | "failure" | "pending";

export interface AuditLog {
	id: string;
	occurred_at: string;
	action: AuditAction | string;
	outcome: AuditOutcome | string;
	initiator: string;
	target: string;
	method: string;
	path: string;
	request_ip?: string;
	user_agent: string;
	duration_ms: number;
	type_uri: string;
	signed: boolean;
}

export interface AuditLogQuery {
	search?: string;
	action?: string;
	outcome?: string;
	start?: string;
	end?: string;
	limit?: number;
	offset?: number;
}
