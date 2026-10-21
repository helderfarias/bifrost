import { createFileRoute } from "@tanstack/react-router";
import { NoPermissionView } from "@/components/noPermissionView";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import APIsPage from "./page";

function RouteComponent() {
	return useRbac(RbacResource.RoutingRules, RbacOperation.View) ? <APIsPage /> : <NoPermissionView entity="APIs" />;
}
export const Route = createFileRoute("/workspace/apis")({ component: RouteComponent });