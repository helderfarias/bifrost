import { createFileRoute } from "@tanstack/react-router";
import APIGatewayPage from "./page";

export const Route = createFileRoute("/workspace/api-gateway")({
	component: APIGatewayPage,
});