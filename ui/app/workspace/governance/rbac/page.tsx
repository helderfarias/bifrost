import PageTitle from "@/components/pageTitle";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { MultiSelect } from "@/components/ui/multiSelect";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { getErrorMessage, useCreateIdentityRoleMutation, useDeleteIdentityRoleMutation, useGetIdentityRolesQuery, useUpdateIdentityRoleMutation } from "@/lib/store";
import type { IdentityRole, IdentityRoleInput } from "@/lib/types/identity";
import { Edit, Plus, ShieldCheck, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

const permissions = [
	{ value: "*", label: "Full administration", description: "All resources and actions" },
	{ value: "users:read", label: "View users" }, { value: "users:write", label: "Manage users" },
	{ value: "roles:read", label: "View roles" }, { value: "roles:write", label: "Manage roles" },
	{ value: "projects:read", label: "View projects" }, { value: "projects:write", label: "Manage projects" },
	{ value: "virtual_keys:read", label: "View virtual keys" }, { value: "virtual_keys:write", label: "Manage virtual keys" },
	{ value: "api_gateway:read", label: "View API Gateway" }, { value: "api_gateway:write", label: "Manage API Gateway" },
];

const newRole = (): IdentityRoleInput => ({ name: "", description: "", external_role: "", permissions: [] });
const roleForm = (role: IdentityRole): IdentityRoleInput => ({ name: role.name, description: role.description, external_role: role.external_role, permissions: role.permissions ?? [] });

export default function GovernanceRbacPage() {
	const { data, isLoading, isError } = useGetIdentityRolesQuery();
	const [createRole, { isLoading: creating }] = useCreateIdentityRoleMutation();
	const [updateRole, { isLoading: updating }] = useUpdateIdentityRoleMutation();
	const [deleteRole] = useDeleteIdentityRoleMutation();
	const [editing, setEditing] = useState<IdentityRole | null>(null);
	const [open, setOpen] = useState(false);
	const [form, setForm] = useState<IdentityRoleInput>(newRole);
	useEffect(() => setForm(editing ? roleForm(editing) : newRole()), [editing, open]);
	const save = async () => {
		if (!form.name.trim() || !form.external_role.trim()) { toast.error("Role name and Keycloak role are required"); return; }
		try {
			if (editing) { await updateRole({ id: editing.id, data: form }).unwrap(); toast.success("Role updated"); }
			else { await createRole(form).unwrap(); toast.success("Role created"); }
			setOpen(false);
		} catch (error) { toast.error(getErrorMessage(error)); }
	};
	const remove = async (role: IdentityRole) => {
		if (!window.confirm(`Remove ${role.name}?`)) return;
		try { await deleteRole(role.id).unwrap(); toast.success("Role removed"); } catch (error) { toast.error(getErrorMessage(error)); }
	};
	return <div className="no-padding-parent mx-auto flex h-[calc(var(--app-content-viewport)_-_var(--app-bottom-padding))] min-h-0 w-full flex-col overflow-hidden p-4">
		<PageTitle title="Roles & Permissions">Define Bifrost permissions and map them to roles carried in Keycloak tokens.</PageTitle>
		<div className="mb-4 flex items-center justify-between gap-3"><div><h1 className="text-lg font-semibold">Roles</h1><p className="text-muted-foreground text-sm">A Keycloak realm role maps to a Bifrost role and its resource permissions.</p></div><Button className="gap-2" onClick={() => { setEditing(null); setOpen(true); }}><Plus className="size-4" /> Add role</Button></div>
		<div className="overflow-hidden rounded-sm border"><Table><TableHeader><TableRow><TableHead>Role</TableHead><TableHead>Keycloak role</TableHead><TableHead>Permissions</TableHead><TableHead>Type</TableHead><TableHead className="text-right">Actions</TableHead></TableRow></TableHeader><TableBody>
			{isLoading ? <TableRow><TableCell colSpan={5} className="text-muted-foreground h-24 text-center">Loading roles…</TableCell></TableRow> : null}
			{isError ? <TableRow><TableCell colSpan={5} className="text-destructive h-24 text-center">Unable to load roles.</TableCell></TableRow> : null}
			{data?.roles.map((role) => <TableRow key={role.id}><TableCell><div className="font-medium">{role.name}</div><div className="text-muted-foreground max-w-80 truncate text-xs">{role.description}</div></TableCell><TableCell className="font-mono text-xs">{role.external_role}</TableCell><TableCell><div className="flex flex-wrap gap-1">{role.permissions.map((permission) => <Badge key={permission} variant="outline">{permission}</Badge>)}</div></TableCell><TableCell>{role.is_system ? <Badge variant="secondary">System</Badge> : <Badge variant="outline">Custom</Badge>}</TableCell><TableCell className="text-right"><Button variant="ghost" size="icon" aria-label={`Edit ${role.name}`} onClick={() => { setEditing(role); setOpen(true); }}><Edit className="size-4" /></Button>{!role.is_system ? <Button variant="ghost" size="icon" aria-label={`Remove ${role.name}`} onClick={() => remove(role)}><Trash2 className="size-4 text-destructive" /></Button> : null}</TableCell></TableRow>)}
		</TableBody></Table></div>
		<Dialog open={open} onOpenChange={setOpen}><DialogContent><DialogHeader><DialogTitle>{editing ? "Edit role" : "Add role"}</DialogTitle><DialogDescription>Tokens containing the Keycloak realm role are synchronized to this Bifrost role.</DialogDescription></DialogHeader><div className="grid gap-4">
			<div className="grid gap-2"><Label htmlFor="role-name">Role name</Label><Input id="role-name" value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} /></div>
			<div className="grid gap-2"><Label htmlFor="keycloak-role">Keycloak realm role</Label><Input id="keycloak-role" placeholder="bifrost-developer" value={form.external_role} onChange={(event) => setForm({ ...form, external_role: event.target.value })} /></div>
			<div className="grid gap-2"><Label htmlFor="role-description">Description</Label><Textarea id="role-description" value={form.description} onChange={(event) => setForm({ ...form, description: event.target.value })} /></div>
			<div className="grid gap-2"><Label>Permissions</Label><MultiSelect key={`${editing?.id ?? "new"}-${open}`} options={permissions} defaultValue={form.permissions} onValueChange={(permissions) => setForm({ ...form, permissions })} placeholder="Select permissions" maxCount={3} /></div>
		</div><DialogFooter><Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button><Button onClick={save} isLoading={creating || updating}><ShieldCheck className="mr-2 size-4" />{editing ? "Save changes" : "Create role"}</Button></DialogFooter></DialogContent></Dialog>
	</div>;
}
