/** One row of the staff form. The rules mirror MemberInput of the backend plug (services/staff.py). */
export interface Member {
  username: string;
  first_name: string;
  last_name: string;
  phone_number: string;
  email: string;
  gender: string;
  role: string;
  department: string;
  invite: boolean;
}

export type MemberProblem = "required" | "email_for_invite";

export const blankMember = (role = "doctor"): Member => ({
  username: "",
  first_name: "",
  last_name: "",
  phone_number: "",
  email: "",
  gender: "female",
  role,
  department: "",
  invite: false,
});

/** False when CARE cannot send the invitation email (options.invite_available of the staff preview). */
export const inviteAvailable = (options?: {
  invite_available?: boolean;
}): boolean => options?.invite_available !== false;

/** The first problem of a row, or null when the backend accepts it. */
export function memberProblem(m: Member): MemberProblem | null {
  if (!m.username.trim() || !m.first_name.trim() || !m.phone_number.trim()) {
    return "required";
  }
  // An invited account has no password, so it needs a real address.
  if (m.invite && !m.email.trim()) return "email_for_invite";
  return null;
}

/** The body of one member for POST steps/staff/apply/. */
export function memberBody(
  m: Member,
  roleDepartments: Record<string, string>,
): Record<string, unknown> {
  const body: Record<string, unknown> = {
    username: m.username.trim(),
    first_name: m.first_name.trim(),
    last_name: m.last_name.trim(),
    phone_number: m.phone_number.trim(),
    gender: m.gender,
    role: m.role,
    department: m.department || roleDepartments[m.role] || "",
    invite: m.invite,
  };
  if (m.email.trim()) body.email = m.email.trim();
  return body;
}
