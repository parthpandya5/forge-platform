import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { StatusBadge } from "./status-badge";

describe("StatusBadge", () => {
  it("renders a readable deployment state", () => {
    render(<StatusBadge status="VERIFYING" />);
    expect(screen.getByText("Verifying")).toBeInTheDocument();
  });
});
