import type { AuditLog, AuditLogQuery } from "@/lib/types/auditLogs";
import { baseApi } from "./baseApi";

export const auditLogsApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getAuditLogs: builder.query<{ audit_logs: AuditLog[]; count: number }, AuditLogQuery | void>({
			query: (params) => ({ url: "/audit-logs", params }),
			providesTags: ["AuditLogs"],
		}),
	}),
});

export const { useGetAuditLogsQuery } = auditLogsApi;
