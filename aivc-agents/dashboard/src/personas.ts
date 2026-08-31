export type Persona = {
  id: string;
  label: string;
  user: string;
  roles: string;
  scopes: string;
  tenant: string;
  blurb: string;
};

export const PERSONAS: Persona[] = [
  {
    id: "employee",
    label: "Employee",
    user: "demo",
    roles: "employee",
    scopes: "corpus:read",
    tenant: "northgate",
    blurb: "Public policies; ACL refuse on HR",
  },
  {
    id: "hr",
    label: "HR BP",
    user: "hr.bp",
    roles: "employee,hr",
    scopes: "corpus:read",
    tenant: "northgate",
    blurb: "Merit budget and restricted HR docs",
  },
  {
    id: "clerk",
    label: "AP clerk",
    user: "ap.clerk",
    roles: "finance",
    scopes: "ap:read",
    tenant: "northgate",
    blurb: "Triage invoices and view the queue",
  },
  {
    id: "controller",
    label: "Controller",
    user: "s.oyelaran",
    roles: "finance",
    scopes: "ap:read",
    tenant: "northgate",
    blurb: "Approve runs — approver must match X-User",
  },
  {
    id: "assistant",
    label: "Full assistant",
    user: "m.lindqvist",
    roles: "employee,finance",
    scopes: "warehouse:read,ap:read,corpus:read",
    tenant: "northgate",
    blurb: "Supervisor with warehouse + AP + corpus",
  },
  {
    id: "contractor",
    label: "Contractor",
    user: "contractor",
    roles: "employee",
    scopes: "corpus:read",
    tenant: "northgate",
    blurb: "Same questions; tools denied by policy",
  },
];

export function personaById(id: string): Persona {
  return PERSONAS.find((p) => p.id === id) ?? PERSONAS[0];
}
