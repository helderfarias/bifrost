import { useState } from "react";
import { z } from "zod";
import { toast } from "sonner";
import PageTitle from "@/components/pageTitle";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { getErrorMessage } from "@/lib/store/apis/baseApi";
import { APIRoute, useGetAPIRoutesQuery, useSaveAPIRoutesMutation } from "@/lib/store/apis/apiGatewayApi";

const routeSchema = z.object({
	id: z.string().min(1).max(120),
	name: z.string().min(1).max(120),
	upstream: z.url().refine((v) => /^https?:\/\//.test(v), "Use an HTTP or HTTPS upstream"),
	path: z.string().regex(/^\/(api\/v2|gateway)\/[^/](?:.*[^/])?$/, "Use a path below /api/v2/ or /gateway/ without a trailing slash"),
	strip_prefix: z.boolean(),
	relay_authorization: z.boolean(),
	enabled: z.boolean(),
	capabilities: z.array(z.string().min(1)).min(1),
	consumers: z
		.array(
			z.object({
				name: z.string().min(1),
				token_sha256: z.string().regex(/^[a-f0-9]{64}$/),
				capabilities: z.array(z.string().min(1)).min(1),
			}),
		)
		.min(1),
	requests_per_minute: z.number().int().min(0).max(1000000),
	timeout_seconds: z.number().int().min(1).max(300),
	failure_threshold: z.number().int().min(1).max(1000),
	recovery_seconds: z.number().int().min(1).max(3600),
});
const newRoute = (): APIRoute => ({
	id: crypto.randomUUID(),
	name: "OCR API",
	upstream: "http://localhost:3000",
	path: "/api/v2/ocr",
	strip_prefix: true,
	relay_authorization: true,
	enabled: true,
	capabilities: ["svc.ocr", "svc.all"],
	consumers: [],
	requests_per_minute: 60,
	timeout_seconds: 30,
	failure_threshold: 5,
	recovery_seconds: 30,
});
const split = (value: string) =>
	value
		.split(",")
		.map((v) => v.trim())
		.filter(Boolean);

export default function APIsPage() {
	const { data, isLoading, error, refetch } = useGetAPIRoutesQuery();
	const [save, { isLoading: saving }] = useSaveAPIRoutesMutation();
	const canCreate = useRbac(RbacResource.RoutingRules, RbacOperation.Create);
	const canUpdate = useRbac(RbacResource.RoutingRules, RbacOperation.Update);
	const canDelete = useRbac(RbacResource.RoutingRules, RbacOperation.Delete);
	const [draft, setDraft] = useState<APIRoute | null>(null);
	const [revision, setRevision] = useState("");
	const [originalID, setOriginalID] = useState<string | null>(null);
	const [consumerName, setConsumerName] = useState("");
	const [consumerToken, setConsumerToken] = useState("");
	const [consumerCaps, setConsumerCaps] = useState("svc.ocr");
	const [message, setMessage] = useState("");
	const edit = (route: APIRoute, existing: boolean) => {
		setDraft(structuredClone(route));
		setRevision(data?.revision ?? "");
		setOriginalID(existing ? route.id : null);
		setMessage("");
		setConsumerName("");
		setConsumerToken("");
	};
	const update = <K extends keyof APIRoute>(key: K, value: APIRoute[K]) => setDraft((r) => (r ? { ...r, [key]: value } : r));
	const addConsumer = async () => {
		if (!draft || !consumerName.trim() || consumerToken.length < 32 || split(consumerCaps).length === 0) {
			setMessage("Enter a consumer name, capabilities and an access key of at least 32 characters.");
			return;
		}
		const bytes = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(consumerToken));
		const token_sha256 = Array.from(new Uint8Array(bytes), (v) => v.toString(16).padStart(2, "0")).join("");
		update("consumers", [...draft.consumers, { name: consumerName.trim(), token_sha256, capabilities: split(consumerCaps) }]);
		setConsumerName("");
		setConsumerToken("");
		setMessage("");
	};
	const submit = async () => {
		const parsed = routeSchema.safeParse(
			draft ? { ...draft, capabilities: draft.capabilities.map((c) => c.trim()).filter(Boolean) } : draft,
		);
		if (!parsed.success) {
			setMessage(parsed.error.issues.map((i) => `${i.path.join(".")}: ${i.message}`).join("; "));
			return;
		}
		if (consumerToken || consumerName) {
			setMessage("Add or clear the pending consumer before saving.");
			return;
		}
		try {
			await save({ revision, routes: [...(data?.routes ?? []).filter((r) => r.id !== originalID), parsed.data] }).unwrap();
			setDraft(null);
			toast.success("API route saved");
		} catch (e) {
			setMessage(getErrorMessage(e));
		}
	};
	const remove = async (route: APIRoute) => {
		if (!data || !window.confirm(`Delete API route ${route.name}?`)) return;
		try {
			await save({ revision: data.revision, routes: data.routes.filter((r) => r.id !== route.id) }).unwrap();
			toast.success("API route deleted");
		} catch (e) {
			toast.error(getErrorMessage(e));
		}
	};
	return (
		<div className="mx-auto flex w-full flex-col gap-6 p-4" data-testid="apis-page">
			<PageTitle title="APIs" />
			<div className="flex items-center justify-between">
				<h2 className="text-lg font-semibold">API routes</h2>
				<Button disabled={!canCreate || saving || !data || !!draft} onClick={() => edit(newRoute(), false)} data-testid="add-api-route">
					Add API
				</Button>
			</div>
			<p className="text-muted-foreground text-sm">
				Consumers send X-Bifrost-API-Key. Authorization can be relayed separately. Quotas and circuit breakers apply per gateway instance.
				Billing is not enabled.
			</p>
			{isLoading && <p>Loading APIs…</p>}
			{!!error && (
				<div role="alert">
					{getErrorMessage(error)}{" "}
					<Button variant="outline" onClick={() => refetch()}>
						Retry
					</Button>
				</div>
			)}
			{data && !data.routes.length && <p data-testid="apis-empty">No APIs configured. Add a route to connect your first upstream.</p>}
			{data?.routes.map((route) => (
				<div key={route.id} className="flex items-center justify-between rounded-md border p-4" data-testid="api-route-row">
					<div>
						<h3 className="font-medium">
							{route.name} <span className="text-muted-foreground text-sm">{route.enabled ? "Enabled" : "Disabled"}</span>
						</h3>
						<p className="font-mono text-sm">
							{route.path} → {route.upstream}
						</p>
						<p className="text-muted-foreground text-sm">
							{route.consumers.length} consumers · {route.requests_per_minute || "Unlimited"} requests/minute per consumer
						</p>
					</div>
					<div className="flex gap-2">
						<Button variant="outline" disabled={!canUpdate || !!draft || saving} onClick={() => edit(route, true)}>
							Edit
						</Button>
						<Button variant="destructive" disabled={!canDelete || !!draft || saving} onClick={() => remove(route)}>
							Delete
						</Button>
					</div>
				</div>
			))}
			{draft && (
				<section className="space-y-4 rounded-md border p-4" aria-label="API route editor">
					<h3 className="font-semibold">{originalID ? "Edit API" : "New API"}</h3>
					{(
						[
							["name", "Name"],
							["upstream", "Upstream URL"],
							["path", "Public path"],
						] as const
					).map(([key, label]) => (
						<div key={key} className="space-y-1">
							<Label htmlFor={`api-${key}`}>{label}</Label>
							<Input id={`api-${key}`} value={draft[key]} onChange={(e) => update(key, e.target.value)} />
						</div>
					))}
					<p className="text-muted-foreground text-sm">
						Paths must be below /api/v2/ or /gateway/. The path and all its subpaths match; the longest matching enabled route wins.
						Upstream URLs may address private services.
					</p>
					<div className="space-y-1">
						<Label htmlFor="api-capabilities">Required capabilities (any match, comma-separated)</Label>
						<Input
							id="api-capabilities"
							value={draft.capabilities.join(",")}
							onChange={(e) => update("capabilities", e.target.value.split(","))}
						/>
					</div>
					{(
						[
							["strip_prefix", "Strip public path prefix"],
							["relay_authorization", "Relay Authorization header"],
							["enabled", "Enabled"],
						] as const
					).map(([key, label]) => (
						<label key={key} className="flex items-center gap-2">
							<input type="checkbox" checked={draft[key]} onChange={(e) => update(key, e.target.checked)} />
							{label}
						</label>
					))}
					<div className="grid grid-cols-2 gap-4">
						{(
							[
								["requests_per_minute", "Requests/minute per consumer (0 = unlimited)"],
								["timeout_seconds", "Timeout (seconds)"],
								["failure_threshold", "Failures before opening circuit"],
								["recovery_seconds", "Circuit recovery (seconds)"],
							] as const
						).map(([key, label]) => (
							<div key={key} className="space-y-1">
								<Label htmlFor={`api-${key}`}>{label}</Label>
								<Input id={`api-${key}`} type="number" value={draft[key]} onChange={(e) => update(key, Number(e.target.value))} />
							</div>
						))}
					</div>
					<h4 className="font-medium">Consumers</h4>
					{draft.consumers.map((c, i) => (
						<div key={c.token_sha256} className="flex items-center justify-between border-b py-2">
							<span>
								{c.name} · {c.capabilities.join(", ")}
							</span>
							<Button
								variant="outline"
								onClick={() =>
									update(
										"consumers",
										draft.consumers.filter((_, index) => index !== i),
									)
								}
							>
								Remove {c.name}
							</Button>
						</div>
					))}
					<div className="space-y-2">
						<Label htmlFor="consumer-name">Consumer name</Label>
						<Input id="consumer-name" value={consumerName} onChange={(e) => setConsumerName(e.target.value)} />
						<Label htmlFor="consumer-token">Access key (at least 32 random characters)</Label>
						<Input
							id="consumer-token"
							type="password"
							autoComplete="new-password"
							value={consumerToken}
							onChange={(e) => setConsumerToken(e.target.value)}
						/>
						<Label htmlFor="consumer-capabilities">Consumer capabilities (comma-separated)</Label>
						<Input id="consumer-capabilities" value={consumerCaps} onChange={(e) => setConsumerCaps(e.target.value)} />
						<p className="text-muted-foreground text-sm">
							Keep the original key securely. Only its SHA-256 hash is saved. Remove and re-add a consumer to rotate its key.
						</p>
						<Button variant="outline" onClick={addConsumer}>
							Add consumer
						</Button>
					</div>
					{message && (
						<p role="alert" className="text-destructive">
							{message}
						</p>
					)}
					<div className="flex gap-2">
						<Button disabled={saving} onClick={submit} data-testid="save-api-route">
							{saving ? "Saving…" : "Save API"}
						</Button>
						<Button variant="outline" disabled={saving} onClick={() => setDraft(null)}>
							Cancel
						</Button>
					</div>
				</section>
			)}
		</div>
	);
}