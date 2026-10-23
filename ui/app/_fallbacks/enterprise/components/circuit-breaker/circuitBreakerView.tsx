import PageTitle from "@/components/pageTitle";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getErrorMessage, useCreateCircuitBreakerPolicyMutation, useDeleteCircuitBreakerPolicyMutation, useGetCircuitBreakerPoliciesQuery, useGetCircuitBreakerStateQuery, useUpdateCircuitBreakerPolicyMutation } from "@/lib/store";
import type { CircuitBreakerPolicy } from "@/lib/types/circuitBreaker";
import { Edit, Plus, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";

type Form = CircuitBreakerPolicy;
const blank = (): Form => ({ name: "", enabled: true, primary_provider: "", primary_model: "", fallback_provider: "", fallback_model: "", default_cooldown: "30s", cooldown_header: "", condition: { operator: "OR", signals: [{ source: "response_header", header_name: "" }] } });

export default function CircuitBreakerView() {
	const { data, isLoading } = useGetCircuitBreakerPoliciesQuery();
	const { data: stateData } = useGetCircuitBreakerStateQuery();
	const [create, { isLoading: creating }] = useCreateCircuitBreakerPolicyMutation();
	const [update] = useUpdateCircuitBreakerPolicyMutation();
	const [remove] = useDeleteCircuitBreakerPolicyMutation();
	const [open, setOpen] = useState(false);
	const [editing, setEditing] = useState<Form | null>(null);
	const [form, setForm] = useState<Form>(blank());
	useEffect(() => setForm(editing ? structuredClone(editing) : blank()), [editing, open]);
	const states = useMemo(() => new Map((stateData?.states ?? []).map((state) => [state.name, state])), [stateData]);
	const save = async () => {
		if (!form.name.trim() || !form.primary_provider.trim() || !form.primary_model.trim() || !form.fallback_provider.trim() || !form.fallback_model.trim() || !form.condition.signals[0]?.header_name.trim()) { toast.error("Name, primary, fallback and a response header are required"); return; }
		try { if (editing) await update(form).unwrap(); else await create(form).unwrap(); toast.success(`Circuit breaker policy ${editing ? "updated" : "created"}`); setOpen(false); } catch (error) { toast.error(getErrorMessage(error)); }
	};
	const toggle = async (policy: Form, enabled: boolean) => { try { await update({ ...policy, enabled }).unwrap(); } catch (error) { toast.error(getErrorMessage(error)); } };
	return <div className="no-padding-parent mx-auto flex h-[calc(var(--app-content-viewport)_-_var(--app-bottom-padding))] min-h-0 w-full flex-col overflow-hidden p-4">
		<PageTitle title="Circuit Breaker">Detect upstream failure headers and redirect traffic to a fallback provider for a controlled cooldown.</PageTitle>
		<div className="mb-4 flex items-center justify-between"><div><h1 className="text-lg font-semibold">Policies</h1><p className="text-muted-foreground text-sm">Circuits close automatically after the configured cooldown.</p></div><Button className="gap-2" onClick={() => { setEditing(null); setOpen(true); }}><Plus className="size-4" /> Add policy</Button></div>
		<div className="overflow-hidden rounded-sm border"><Table><TableHeader><TableRow><TableHead>Policy</TableHead><TableHead>Primary</TableHead><TableHead>Fallback</TableHead><TableHead>Signal</TableHead><TableHead>Status</TableHead><TableHead className="text-right">Actions</TableHead></TableRow></TableHeader><TableBody>
			{isLoading ? <TableRow><TableCell colSpan={6} className="h-24 text-center text-muted-foreground">Loading policies…</TableCell></TableRow> : null}
			{!isLoading && !(data?.policies?.length ?? 0) ? <TableRow><TableCell colSpan={6} className="h-28 text-center text-muted-foreground">No circuit breaker policies configured.</TableCell></TableRow> : null}
			{data?.policies?.map((policy) => { const state = states.get(policy.name); const signal = policy.condition.signals[0]; return <TableRow key={policy.name}><TableCell className="font-medium">{policy.name}</TableCell><TableCell className="font-mono text-xs">{policy.primary_provider}/{policy.primary_model}</TableCell><TableCell className="font-mono text-xs">{policy.fallback_provider}/{policy.fallback_model}</TableCell><TableCell className="font-mono text-xs">{signal?.header_name}{signal?.header_value ? ` = ${signal.header_value}` : signal?.header_contains ? ` contains ${signal.header_contains}` : " present"}</TableCell><TableCell><div className="flex items-center gap-2"><Switch checked={policy.enabled} onAsyncCheckedChange={(enabled) => toggle(policy, enabled)} /><Badge variant={state?.open ? "destructive" : policy.enabled ? "default" : "secondary"}>{state?.open ? "Open" : policy.enabled ? "Closed" : "Disabled"}</Badge></div></TableCell><TableCell className="text-right"><Button variant="ghost" size="icon" onClick={() => { setEditing(policy); setOpen(true); }}><Edit className="size-4" /></Button><Button variant="ghost" size="icon" onClick={async () => { try { await remove(policy.name).unwrap(); toast.success("Circuit breaker policy deleted"); } catch (error) { toast.error(getErrorMessage(error)); } }}><Trash2 className="size-4 text-destructive" /></Button></TableCell></TableRow>;})}
		</TableBody></Table></div>
		<Dialog open={open} onOpenChange={setOpen}><DialogContent><DialogHeader><DialogTitle>{editing ? "Edit circuit breaker" : "Add circuit breaker"}</DialogTitle><DialogDescription>Open the circuit when the configured upstream response header matches.</DialogDescription></DialogHeader><div className="grid gap-3"><div className="grid gap-2"><Label>Name</Label><Input disabled={!!editing} value={form.name} placeholder="anthropic-overloaded" onChange={(event) => setForm({ ...form, name: event.target.value })} /></div><div className="grid grid-cols-2 gap-3"><div className="grid gap-2"><Label>Primary provider</Label><Input value={form.primary_provider} placeholder="anthropic" onChange={(event) => setForm({ ...form, primary_provider: event.target.value })} /></div><div className="grid gap-2"><Label>Primary model</Label><Input value={form.primary_model} placeholder="claude-sonnet-4" onChange={(event) => setForm({ ...form, primary_model: event.target.value })} /></div><div className="grid gap-2"><Label>Fallback provider</Label><Input value={form.fallback_provider} placeholder="openai" onChange={(event) => setForm({ ...form, fallback_provider: event.target.value })} /></div><div className="grid gap-2"><Label>Fallback model</Label><Input value={form.fallback_model} placeholder="gpt-4.1" onChange={(event) => setForm({ ...form, fallback_model: event.target.value })} /></div></div><div className="grid gap-2"><Label>Primary key IDs (optional, comma-separated)</Label><Input value={(form.primary_key_ids ?? []).join(", ")} placeholder="provider-key-a, provider-key-b" onChange={(event) => setForm({ ...form, primary_key_ids: event.target.value.split(",").map((value) => value.trim()).filter(Boolean) })} /></div><div className="grid grid-cols-2 gap-3"><div className="grid gap-2"><Label>Response header</Label><Input value={form.condition.signals[0]?.header_name ?? ""} placeholder="retry-after" onChange={(event) => setForm({ ...form, condition: { ...form.condition, signals: [{ ...form.condition.signals[0], source: "response_header", header_name: event.target.value }] } })} /></div><div className="grid gap-2"><Label>Header value (optional)</Label><Input value={form.condition.signals[0]?.header_value ?? ""} placeholder="60" onChange={(event) => setForm({ ...form, condition: { ...form.condition, signals: [{ ...form.condition.signals[0], header_value: event.target.value, header_contains: "" }] } })} /></div><div className="grid gap-2"><Label>Cooldown</Label><Input value={form.default_cooldown ?? ""} placeholder="30s" onChange={(event) => setForm({ ...form, default_cooldown: event.target.value })} /></div><div className="grid gap-2"><Label>Cooldown header (ms)</Label><Input value={form.cooldown_header ?? ""} placeholder="retry-after-ms" onChange={(event) => setForm({ ...form, cooldown_header: event.target.value })} /></div></div></div><DialogFooter><Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button><Button onClick={save} disabled={creating}>{editing ? "Save changes" : "Create policy"}</Button></DialogFooter></DialogContent></Dialog>
	</div>;
}
