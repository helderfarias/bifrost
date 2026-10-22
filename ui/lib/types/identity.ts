export type IdentityUser = {
	id: string;
	subject: string;
	username: string;
	email: string;
	name: string;
	source: string;
	is_active: boolean;
	role_ids: string[];
};

export type IdentityRole = {
	id: string;
	name: string;
	description: string;
	external_role: string;
	permissions: string[];
	is_system: boolean;
};

export type IdentityUserInput = Omit<IdentityUser, "id">;
export type IdentityRoleInput = Omit<IdentityRole, "id" | "is_system">;
