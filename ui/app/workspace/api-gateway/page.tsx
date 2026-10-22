import PageTitle from "@/components/pageTitle";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/ui/alertDialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { MultiSelect } from "@/components/ui/multiSelect";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import {
	getErrorMessage,
	useCreateAPIGatewayRouteMutation,
	useDeleteAPIGatewayRouteMutation,
	useGetAPIGatewayRoutesQuery,
	useGetVirtualKeysQuery,
	useUpdateAPIGatewayRouteMutation,
} from "@/lib/store";
import type { APIGatewayRoute, APIGatewayRouteRequest } from "@/lib/types/apiGateway";
import { Edit, Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

type RouteForm = {
	id: string;
	uri: string;
	path: string;
	rewritePath: string;
	virtualKeyIDs: string[];
	enabled: boolean;
};

const newRouteForm = (): RouteForm => ({ id: "", uri: "", path: "", rewritePath: "", virtualKeyIDs: [], enabled: true });

const toForm = (route: APIGatewayRoute): RouteForm => ({
	id: route.id,
	uri: route.uri,
	path: route.predicates.find((predicate) => predicate.startsWith("Path="))?.slice(5) ?? "",
	rewritePath: route.filters?.find((filter) => filter.startsWith("RewritePath="))?.slice("RewritePath=".length) ?? "",
	virtualKeyIDs: route.virtual_key_ids ?? [],
	enabled: route.enabled,
});

const toRequest = (form: RouteForm): APIGatewayRouteRequest => ({
	id: form.id.trim(),
	uri: form.uri.trim(),
	predicates: [`Path=${form.path.trim()}`],
	filters: form.rewritePath.trim() ? [`RewritePath=${form.rewritePath.trim()}`] : [],
	virtual_key_ids: form.virtualKeyIDs,
	enabled: form.enabled,
});

const publicPath = (path: string) => `/apis${path.startsWith("/") ? path : `/${path}`}`;

export default function APIGatewayPage() {
	const { data, isLoading, isError } = useGetAPIGatewayRoutesQuery();
	const { data: virtualKeysData } = useGetVirtualKeysQuery({ limit: 1000 });
	const [createRoute, { isLoading: isCreating }] = useCreateAPIGatewayRouteMutation();
	const [updateRoute] = useUpdateAPIGatewayRouteMutation();
	const [deleteRoute, { isLoading: isDeleting }] = useDeleteAPIGatewayRouteMutation();
	const [dialogOpen, setDialogOpen] = useState(false);
	const [editingRoute, setEditingRoute] = useState<APIGatewayRoute | null>(null);
	const [routeToDelete, setRouteToDelete] = useState<APIGatewayRoute | null>(null);
	const [form, setForm] = useState<RouteForm>(newRouteForm);

	useEffect(() => {
		setForm(editingRoute ? toForm(editingRoute) : newRouteForm());
	}, [editingRoute, dialogOpen]);

	const saveRoute = async () => {
		if (!form.id.trim() || !form.uri.trim() || !form.path.trim()) {
			toast.error("Route ID, upstream URL and path are required");
			return;
		}
		const payload = toRequest(form);
		try {
			if (editingRoute) {
				await updateRoute({ id: editingRoute.id, data: payload }).unwrap();
				toast.success("API route updated");
			} else {
				await createRoute(payload).unwrap();
				toast.success("API route created");
			}
			setDialogOpen(false);
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	const toggleRoute = async (route: APIGatewayRoute, enabled: boolean) => {
		try {
			await updateRoute({ id: route.id, data: { ...route, enabled } }).unwrap();
			toast.success(`Route ${enabled ? "enabled" : "disabled"}`);
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	const removeRoute = async () => {
		if (!routeToDelete) return;
		try {
			await deleteRoute(routeToDelete.id).unwrap();
			toast.success("API route deleted");
			setRouteToDelete(null);
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	const routes = data?.routes ?? [];
	const virtualKeyOptions = (virtualKeysData?.virtual_keys ?? []).map((virtualKey) => ({
		value: virtualKey.id,
		label: virtualKey.name,
		description: virtualKey.is_active ? virtualKey.id : `${virtualKey.id} (inactive)`,
		disabled: !virtualKey.is_active,
	}));
	return (
		<div className="no-padding-parent mx-auto flex h-[calc(var(--app-content-viewport)_-_var(--app-bottom-padding))] min-h-0 w-full flex-col overflow-hidden p-4">
			<PageTitle title="API Gateway">
				Register upstream HTTP APIs under <code>/apis/&lt;endpoint&gt;</code>, isolated from Bifrost chat and management endpoints.
			</PageTitle>
			<div className="mb-4 flex flex-wrap items-center justify-between gap-3">
				<div>
					<h1 className="text-lg font-semibold">API routes</h1>
					<p className="text-muted-foreground text-sm">Every registered endpoint is exposed under the fixed <code>/apis</code> prefix.</p>
				</div>
				<Button
					data-testid="api-gateway-add-route-btn"
					onClick={() => {
						setEditingRoute(null);
						setDialogOpen(true);
					}}
					className="gap-2"
				>
					<Plus className="size-4" /> Add API
				</Button>
			</div>

			<div className="overflow-hidden rounded-sm border">
				<Table data-testid="api-gateway-routes-table">
					<TableHeader>
						<TableRow>
							<TableHead>ID</TableHead>
							<TableHead>Public endpoint</TableHead>
							<TableHead>Upstream</TableHead>
							<TableHead>Rewrite</TableHead>
							<TableHead>Status</TableHead>
							<TableHead className="text-right">Actions</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{isLoading ? (
							<TableRow>
								<TableCell colSpan={6} className="text-muted-foreground h-24 text-center">
									Loading API routes…
								</TableCell>
							</TableRow>
						) : null}
						{isError ? (
							<TableRow>
								<TableCell colSpan={6} className="text-destructive h-24 text-center">
									Unable to load API routes.
								</TableCell>
							</TableRow>
						) : null}
						{!isLoading && !isError && routes.length === 0 ? (
							<TableRow>
								<TableCell colSpan={6} className="text-muted-foreground h-32 text-center">
									No APIs registered. Add one to start proxying requests.
								</TableCell>
							</TableRow>
						) : null}
						{routes.map((route) => (
							<TableRow key={route.id}>
								<TableCell className="font-medium">{route.id}</TableCell>
								<TableCell className="font-mono text-xs">{publicPath(route.predicates.find((predicate) => predicate.startsWith("Path="))?.slice(5) ?? "/")}</TableCell>
								<TableCell className="max-w-72 truncate font-mono text-xs" title={route.uri}>
									{route.uri}
								</TableCell>
								<TableCell className="max-w-72 truncate font-mono text-xs" title={(route.filters ?? []).join(", ")}>
									{(route.filters ?? []).join(", ") || "—"}
								</TableCell>
								<TableCell>
									<div className="flex items-center gap-2">
										<Switch
											checked={route.enabled}
											onAsyncCheckedChange={(enabled) => toggleRoute(route, enabled)}
											data-testid={`api-gateway-enabled-${route.id}-switch`}
										/>
										<Badge variant={route.enabled ? "default" : "secondary"}>{route.enabled ? "Enabled" : "Disabled"}</Badge>
										{route.virtual_key_ids?.length ? (
											<Badge variant="outline">
												{route.virtual_key_ids.length} key{route.virtual_key_ids.length === 1 ? "" : "s"}
											</Badge>
										) : null}
									</div>
								</TableCell>
								<TableCell className="text-right">
									<div className="flex justify-end gap-1">
										<Button
											variant="ghost"
											size="icon"
											aria-label={`Edit ${route.id}`}
											onClick={() => {
												setEditingRoute(route);
												setDialogOpen(true);
											}}
										>
											<Edit className="size-4" />
										</Button>
										<Button variant="ghost" size="icon" aria-label={`Delete ${route.id}`} onClick={() => setRouteToDelete(route)}>
											<Trash2 className="text-destructive size-4" />
										</Button>
									</div>
								</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
			</div>

			<Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
				<DialogContent>
					<DialogHeader>
						<DialogTitle>{editingRoute ? "Edit API route" : "Add API route"}</DialogTitle>
						<DialogDescription>
							Each route is exposed below <code>/apis</code>; use an endpoint path and optional regular-expression rewrite, such as the Spring Cloud Gateway format.
						</DialogDescription>
					</DialogHeader>
					<div className="grid gap-4">
						<div className="grid gap-2">
							<Label htmlFor="api-route-id">Route ID</Label>
							<Input
								id="api-route-id"
								value={form.id}
								disabled={!!editingRoute}
								placeholder="health-proxy"
								onChange={(event) => setForm({ ...form, id: event.target.value })}
							/>
						</div>
						<div className="grid gap-2">
							<Label htmlFor="api-route-uri">Upstream URL</Label>
							<Input
								id="api-route-uri"
								value={form.uri}
								placeholder="http://localhost:7001"
								onChange={(event) => setForm({ ...form, uri: event.target.value })}
							/>
						</div>
						<div className="grid gap-2">
							<Label htmlFor="api-route-path">Endpoint path</Label>
							<Input
								id="api-route-path"
								value={form.path}
								placeholder="/health"
								onChange={(event) => setForm({ ...form, path: event.target.value })}
							/>
							<p className="text-muted-foreground text-xs">
								Exposed as <code>/apis{form.path || "/health"}</code> and saved as <code>Path=/health</code>. Wildcards such as <code>/api/**</code> are supported.
							</p>
						</div>
						<div className="grid gap-2">
							<Label htmlFor="api-route-rewrite">Rewrite path (optional)</Label>
							<Textarea
								id="api-route-rewrite"
								value={form.rewritePath}
								placeholder="/health,/actuator/health"
								className="min-h-20 font-mono text-xs"
								onChange={(event) => setForm({ ...form, rewritePath: event.target.value })}
							/>
							<p className="text-muted-foreground text-xs">
								Applied after removing <code>/apis</code>, for example <code>RewritePath=/health,/actuator/health</code>.
							</p>
						</div>
						<div className="grid gap-2">
							<Label>Allowed virtual keys</Label>
							<MultiSelect
								key={`${editingRoute?.id ?? "new"}-${dialogOpen}`}
								options={virtualKeyOptions}
								defaultValue={form.virtualKeyIDs}
								onValueChange={(virtualKeyIDs) => setForm({ ...form, virtualKeyIDs })}
								placeholder="Public route (no virtual key required)"
								maxCount={2}
							/>
							<p className="text-muted-foreground text-xs">
								When keys are selected, requests must present one of them with <code>x-bf-vk</code> or <code>Authorization: Bearer</code>.
							</p>
						</div>
						<div className="flex items-center gap-3">
							<Switch id="api-route-enabled" checked={form.enabled} onCheckedChange={(enabled) => setForm({ ...form, enabled })} />
							<Label htmlFor="api-route-enabled">Enable this route immediately</Label>
						</div>
					</div>
					<DialogFooter>
						<Button variant="outline" onClick={() => setDialogOpen(false)}>
							Cancel
						</Button>
						<Button onClick={saveRoute} isLoading={isCreating}>
							{editingRoute ? "Save changes" : "Create route"}
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>

			<AlertDialog open={!!routeToDelete} onOpenChange={(open) => !open && setRouteToDelete(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Delete API route?</AlertDialogTitle>
						<AlertDialogDescription>
							Requests to <code>{routeToDelete?.id}</code> will stop being proxied immediately.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel disabled={isDeleting}>Cancel</AlertDialogCancel>
						<AlertDialogAction disabled={isDeleting} onClick={removeRoute}>
							Delete route
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}
