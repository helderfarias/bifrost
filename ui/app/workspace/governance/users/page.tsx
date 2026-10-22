import PageTitle from "@/components/pageTitle";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { MultiSelect } from "@/components/ui/multiSelect";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getErrorMessage, useDeleteIdentityUserMutation, useGetIdentityRolesQuery, useGetIdentityUsersQuery, useGetVirtualKeysQuery, useProvisionKeycloakUserMutation, useReplaceIdentityUserVirtualKeyMutation, useSyncKeycloakUsersMutation, useUpdateIdentityUserMutation } from "@/lib/store";
import type { IdentityUser, IdentityUserInput, KeycloakProvisionUserInput } from "@/lib/types/identity";
import { Edit, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

type UserForm = IdentityUserInput & { temporary_password: string };
const newUser = (): UserForm => ({ subject: "", username: "", email: "", name: "", source: "keycloak", is_active: true, role_ids: [], temporary_password: "" });
const userForm = (user: IdentityUser): UserForm => ({ ...user, role_ids: user.role_ids ?? [], temporary_password: "" });

export default function GovernanceUsersPage() {
	const { data: usersData, isLoading, isError } = useGetIdentityUsersQuery();
	const { data: rolesData } = useGetIdentityRolesQuery();
	const { data: virtualKeysData } = useGetVirtualKeysQuery({ limit: 100 });
	const [provisionKeycloakUser, { isLoading: creating }] = useProvisionKeycloakUserMutation();
	const [updateUser, { isLoading: updating }] = useUpdateIdentityUserMutation();
	const [replaceUserVirtualKey, { isLoading: assigningKey }] = useReplaceIdentityUserVirtualKeyMutation();
	const [deleteUser] = useDeleteIdentityUserMutation();
	const [syncKeycloakUsers, { isLoading: syncing }] = useSyncKeycloakUsersMutation();
	const [editing, setEditing] = useState<IdentityUser | null>(null);
	const [open, setOpen] = useState(false);
	const [form, setForm] = useState<UserForm>(newUser);

	useEffect(() => setForm(editing ? userForm(editing) : newUser()), [editing, open]);
	const roles = rolesData?.roles ?? [];
	const virtualKeys = (virtualKeysData?.virtual_keys ?? []).filter((key) => !key.assigned_user || key.assigned_user.id === editing?.id);
	const options = roles.map((role) => ({ value: role.id, label: role.name, description: role.description }));
	const namesFor = (ids: string[]) => ids.map((id) => roles.find((role) => role.id === id)?.name ?? id);
	const save = async () => {
		if (!form.name.trim() || !form.username.trim() || !form.email.trim()) { toast.error("Name, username and email are required"); return; }
		if (!editing && form.temporary_password.length < 12) { toast.error("Temporary password must contain at least 12 characters"); return; }
		try {
			if (editing) {
				const { virtual_key_id, ...user } = form;
				await updateUser({ id: editing.id, data: user }).unwrap();
				if ((virtual_key_id ?? null) !== (editing.virtual_key_id ?? null)) await replaceUserVirtualKey({ id: editing.id, virtual_key_id: virtual_key_id || null }).unwrap();
				toast.success("User updated");
			}
			else { const { temporary_password, ...user } = form; await provisionKeycloakUser({ ...user, temporary_password } as KeycloakProvisionUserInput).unwrap(); toast.success("User provisioned in Keycloak. They must change the temporary password at first login."); }
			setOpen(false);
		} catch (error) { toast.error(getErrorMessage(error)); }
	};
	const sync = async () => {
		try {
			const result = await syncKeycloakUsers().unwrap();
			toast.success(`Keycloak sync complete: ${result.created} created, ${result.updated} updated`);
		} catch (error) { toast.error(getErrorMessage(error)); }
	};
	const remove = async (user: IdentityUser) => {
		if (!window.confirm(`Remove ${user.name}? This does not delete the user from Keycloak.`)) return;
		try { await deleteUser(user.id).unwrap(); toast.success("User removed"); } catch (error) { toast.error(getErrorMessage(error)); }
	};

	return <div className="no-padding-parent mx-auto flex h-[calc(var(--app-content-viewport)_-_var(--app-bottom-padding))] min-h-0 w-full flex-col overflow-hidden p-4">
		<PageTitle title="Users">Manage identities, Bifrost roles, and personal Virtual Keys.</PageTitle>
		<div className="mb-4 flex items-center justify-between gap-3"><div><h1 className="text-lg font-semibold">Users</h1><p className="text-muted-foreground text-sm">Administrators provision users in Keycloak with a temporary password.</p></div><div className="flex gap-2"><Button variant="outline" className="gap-2" onClick={sync} isLoading={syncing}><RefreshCw className="size-4" /> Sync Keycloak</Button><Button className="gap-2" onClick={() => { setEditing(null); setOpen(true); }}><Plus className="size-4" /> Add user</Button></div></div>
		<div className="overflow-hidden rounded-sm border"><Table><TableHeader><TableRow><TableHead>User</TableHead><TableHead>Email</TableHead><TableHead>Roles</TableHead><TableHead>Virtual Key</TableHead><TableHead>Source</TableHead><TableHead>Status</TableHead><TableHead className="text-right">Actions</TableHead></TableRow></TableHeader><TableBody>
			{isLoading ? <TableRow><TableCell colSpan={7} className="text-muted-foreground h-24 text-center">Loading users…</TableCell></TableRow> : null}
			{isError ? <TableRow><TableCell colSpan={7} className="text-destructive h-24 text-center">Unable to load users.</TableCell></TableRow> : null}
			{!isLoading && !isError && !(usersData?.users.length) ? <TableRow><TableCell colSpan={7} className="text-muted-foreground h-24 text-center">No users yet.</TableCell></TableRow> : null}
			{usersData?.users.map((user) => <TableRow key={user.id}><TableCell><div className="font-medium">{user.name}</div><div className="text-muted-foreground text-xs">{user.username}</div></TableCell><TableCell>{user.email}</TableCell><TableCell><div className="flex flex-wrap gap-1">{namesFor(user.role_ids).map((name) => <Badge key={name} variant="outline">{name}</Badge>)}</div></TableCell><TableCell>{user.virtual_key_id ? virtualKeysData?.virtual_keys.find((key) => key.id === user.virtual_key_id)?.name ?? user.virtual_key_id : <span className="text-muted-foreground">None</span>}</TableCell><TableCell><Badge variant="secondary">{user.source}</Badge></TableCell><TableCell><Badge variant={user.is_active ? "default" : "secondary"}>{user.is_active ? "Active" : "Inactive"}</Badge></TableCell><TableCell className="text-right"><Button variant="ghost" size="icon" aria-label={`Edit ${user.name}`} onClick={() => { setEditing(user); setOpen(true); }}><Edit className="size-4" /></Button><Button variant="ghost" size="icon" aria-label={`Remove ${user.name}`} onClick={() => remove(user)}><Trash2 className="size-4 text-destructive" /></Button></TableCell></TableRow>)}
		</TableBody></Table></div>
		<Dialog open={open} onOpenChange={setOpen}><DialogContent><DialogHeader><DialogTitle>{editing ? "Edit user" : "Add user"}</DialogTitle><DialogDescription>Roles grant the permissions configured under Roles & Permissions.</DialogDescription></DialogHeader><div className="grid gap-4">
			<div className="grid gap-2"><Label htmlFor="identity-name">Name</Label><Input id="identity-name" value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} /></div>
			<div className="grid gap-2"><Label htmlFor="identity-username">Username</Label><Input id="identity-username" value={form.username} onChange={(event) => setForm({ ...form, username: event.target.value })} /></div>
			<div className="grid gap-2"><Label htmlFor="identity-email">Email</Label><Input id="identity-email" type="email" value={form.email} onChange={(event) => setForm({ ...form, email: event.target.value })} /></div>
			{!editing ? <div className="grid gap-2"><Label htmlFor="identity-temporary-password">Temporary password</Label><Input id="identity-temporary-password" type="password" autoComplete="new-password" value={form.temporary_password} onChange={(event) => setForm({ ...form, temporary_password: event.target.value })} /><p className="text-muted-foreground text-xs">The user must change this password at the first Keycloak login. It is never stored in Bifrost.</p></div> : null}
			{editing ? <div className="grid gap-2"><Label>OIDC subject</Label><Input value={form.subject} disabled /></div> : null}
			{editing ? <div className="grid gap-2"><Label>Personal Virtual Key</Label><Select value={form.virtual_key_id ?? "none"} onValueChange={(virtual_key_id) => setForm({ ...form, virtual_key_id: virtual_key_id === "none" ? null : virtual_key_id })}><SelectTrigger className="w-full"><SelectValue placeholder="No Virtual Key" /></SelectTrigger><SelectContent><SelectItem value="none">No Virtual Key</SelectItem>{virtualKeys.map((key) => <SelectItem key={key.id} value={key.id}>{key.name}</SelectItem>)}</SelectContent></Select><p className="text-muted-foreground text-xs">A Virtual Key can be assigned to only one user. Keep it secret; the gateway treats it as a bearer credential.</p></div> : null}
			<div className="grid gap-2"><Label>Roles</Label><MultiSelect key={`${editing?.id ?? "new"}-${open}`} options={options} defaultValue={form.role_ids} onValueChange={(role_ids) => setForm({ ...form, role_ids })} placeholder="No role assigned" /></div>
			<div className="flex items-center gap-3"><Switch id="identity-active" checked={form.is_active} onCheckedChange={(is_active) => setForm({ ...form, is_active })} /><Label htmlFor="identity-active">Active user</Label></div>
		</div><DialogFooter><Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button><Button onClick={save} isLoading={creating || updating || assigningKey}>{editing ? "Save changes" : "Create user"}</Button></DialogFooter></DialogContent></Dialog>
	</div>;
}
