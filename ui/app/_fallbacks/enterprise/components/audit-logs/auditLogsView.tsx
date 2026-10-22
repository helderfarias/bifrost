import PageTitle from "@/components/pageTitle";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useGetAuditLogsQuery } from "@/lib/store";
import type { AuditLogQuery } from "@/lib/types/auditLogs";
import { getApiBaseUrl } from "@/lib/utils/port";
import { Download, RefreshCw, Search } from "lucide-react";
import { useMemo, useState } from "react";

const pageSize = 100;

function toRFC3339(value: string, end = false) {
	if (!value) return undefined;
	return new Date(`${value}${end ? "T23:59:59.999" : "T00:00:00.000"}`).toISOString();
}

function outcomeVariant(outcome: string) {
	if (outcome === "success") return "default" as const;
	if (outcome === "failure") return "destructive" as const;
	return "secondary" as const;
}

export default function AuditLogsView() {
	const [search, setSearch] = useState("");
	const [searchInput, setSearchInput] = useState("");
	const [action, setAction] = useState("");
	const [outcome, setOutcome] = useState("");
	const [start, setStart] = useState("");
	const [end, setEnd] = useState("");
	const [offset, setOffset] = useState(0);
	const query = useMemo<AuditLogQuery>(() => ({ search: search || undefined, action: action || undefined, outcome: outcome || undefined, start: toRFC3339(start), end: toRFC3339(end, true), limit: pageSize, offset }), [action, end, offset, outcome, search, start]);
	const { data, isLoading, isFetching, isError, refetch } = useGetAuditLogsQuery(query);
	const applySearch = () => { setOffset(0); setSearch(searchInput.trim()); };
	const exportLogs = (format: "json" | "jsonl" | "syslog") => {
		const params = new URLSearchParams();
		Object.entries({ ...query, format }).forEach(([key, value]) => { if (value !== undefined && value !== "") params.set(key, String(value)); });
		window.open(`${getApiBaseUrl()}/audit-logs/export?${params}`, "_blank", "noopener,noreferrer");
	};
	const events = data?.audit_logs ?? [];
	const total = data?.count ?? 0;

	return <div className="no-padding-parent mx-auto flex h-[calc(var(--app-content-viewport)_-_var(--app-bottom-padding))] min-h-0 w-full flex-col overflow-hidden p-4">
		<PageTitle title="Audit Logs">Review administrative activity, filtered by actor, action and outcome.</PageTitle>
		<div className="mb-4 flex flex-wrap items-start justify-between gap-3">
			<div><h1 className="text-lg font-semibold">Audit Logs</h1><p className="text-muted-foreground text-sm">Administrative changes are recorded without storing request bodies or credentials.</p></div>
			<div className="flex items-center gap-2"><Button variant="outline" size="sm" className="gap-2" onClick={() => refetch()} isLoading={isFetching}><RefreshCw className="size-4" /> Refresh</Button><Button variant="outline" size="sm" className="gap-2" onClick={() => exportLogs("json")}><Download className="size-4" /> Export JSON</Button></div>
		</div>
		<div className="mb-4 grid gap-2 rounded-md border bg-muted/20 p-3 md:grid-cols-6">
			<div className="flex gap-2 md:col-span-2"><Input value={searchInput} onChange={(event) => setSearchInput(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") applySearch(); }} placeholder="Search initiator, target, IP or path" /><Button variant="secondary" size="icon" aria-label="Search audit logs" onClick={applySearch}><Search className="size-4" /></Button></div>
			<select className="border-input bg-background h-9 rounded-md border px-3 text-sm" value={action} onChange={(event) => { setOffset(0); setAction(event.target.value); }}><option value="">All actions</option><option value="create">Create</option><option value="update">Update</option><option value="delete">Delete</option><option value="authenticate">Authenticate</option><option value="authorize">Authorize</option><option value="export">Export</option></select>
			<select className="border-input bg-background h-9 rounded-md border px-3 text-sm" value={outcome} onChange={(event) => { setOffset(0); setOutcome(event.target.value); }}><option value="">All outcomes</option><option value="success">Success</option><option value="failure">Failure</option><option value="pending">Pending</option></select>
			<Input aria-label="Start date" type="date" value={start} onChange={(event) => { setOffset(0); setStart(event.target.value); }} />
			<Input aria-label="End date" type="date" value={end} onChange={(event) => { setOffset(0); setEnd(event.target.value); }} />
		</div>
		<div className="min-h-0 flex-1 overflow-auto rounded-md border"><Table><TableHeader><TableRow><TableHead>Time</TableHead><TableHead>Action</TableHead><TableHead>Outcome</TableHead><TableHead>Initiator</TableHead><TableHead>Target</TableHead><TableHead>Path</TableHead><TableHead>IP</TableHead><TableHead>Duration</TableHead></TableRow></TableHeader><TableBody>
			{isLoading ? <TableRow><TableCell colSpan={8} className="text-muted-foreground h-24 text-center">Loading audit logs…</TableCell></TableRow> : null}
			{isError ? <TableRow><TableCell colSpan={8} className="text-destructive h-24 text-center">Unable to load audit logs.</TableCell></TableRow> : null}
			{!isLoading && !isError && events.length === 0 ? <TableRow><TableCell colSpan={8} className="text-muted-foreground h-24 text-center">No events match these filters.</TableCell></TableRow> : null}
			{events.map((event) => <TableRow key={event.id}><TableCell className="whitespace-nowrap text-xs">{new Date(event.occurred_at).toLocaleString()}</TableCell><TableCell><Badge variant="outline">{event.action}</Badge></TableCell><TableCell><Badge variant={outcomeVariant(event.outcome)}>{event.outcome}</Badge></TableCell><TableCell>{event.initiator}</TableCell><TableCell className="max-w-48 truncate" title={event.target}>{event.target}</TableCell><TableCell className="max-w-56 truncate font-mono text-xs" title={event.path}>{event.method} {event.path}</TableCell><TableCell className="font-mono text-xs">{event.request_ip || "—"}</TableCell><TableCell>{event.duration_ms} ms</TableCell></TableRow>)}
		</TableBody></Table></div>
		<div className="text-muted-foreground mt-3 flex items-center justify-between text-sm"><span>{total} event{total === 1 ? "" : "s"}</span><div className="flex gap-2"><Button variant="outline" size="sm" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - pageSize))}>Previous</Button><Button variant="outline" size="sm" disabled={offset + events.length >= total} onClick={() => setOffset(offset + pageSize)}>Next</Button></div></div>
		<div className="mt-2 flex justify-end gap-2"><Button variant="ghost" size="sm" onClick={() => exportLogs("jsonl")}>JSON Lines</Button><Button variant="ghost" size="sm" onClick={() => exportLogs("syslog")}>Syslog</Button></div>
	</div>;
}
