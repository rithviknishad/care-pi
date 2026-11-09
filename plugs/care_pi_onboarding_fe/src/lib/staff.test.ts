import { describe, expect, it } from "vitest";

import {
  blankMember,
  inviteAvailable,
  memberBody,
  memberProblem,
} from "@/lib/staff";

const filled = {
  ...blankMember("doctor"),
  username: "dr-anil",
  first_name: "Anil",
  phone_number: "+919446000001",
};

describe("memberProblem", () => {
  it("needs a username, a first name, and a phone number", () => {
    expect(memberProblem(blankMember())).toBe("required");
    expect(memberProblem(filled)).toBeNull();
  });

  it("needs an email when the invite is on", () => {
    expect(memberProblem({ ...filled, invite: true })).toBe("email_for_invite");
    expect(memberProblem({ ...filled, invite: true, email: "  " })).toBe(
      "email_for_invite",
    );
    expect(
      memberProblem({ ...filled, invite: true, email: "anil@clinic.example" }),
    ).toBeNull();
  });
});

describe("memberBody", () => {
  it("trims the fields, sends the email only when given, and keeps the invite", () => {
    expect(
      memberBody(
        { ...filled, username: " dr-anil " },
        { doctor: "General Medicine" },
      ),
    ).toEqual({
      username: "dr-anil",
      first_name: "Anil",
      last_name: "",
      phone_number: "+919446000001",
      gender: "female",
      role: "doctor",
      department: "General Medicine",
      invite: false,
    });
    expect(
      memberBody(
        { ...filled, invite: true, email: " anil@clinic.example " },
        {},
      ),
    ).toMatchObject({
      invite: true,
      email: "anil@clinic.example",
      department: "",
    });
  });
});

describe("inviteAvailable", () => {
  it("is false only when the backend says that CARE cannot send email", () => {
    expect(inviteAvailable({ invite_available: false })).toBe(false);
    expect(inviteAvailable({ invite_available: true })).toBe(true);
    expect(inviteAvailable({})).toBe(true);
    expect(inviteAvailable(undefined)).toBe(true);
  });
});
