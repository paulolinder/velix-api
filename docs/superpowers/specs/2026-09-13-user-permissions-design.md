# Design: Permissões granulares de usuário + registro fechado

Data: 2026-09-13
Status: aprovado para implementação

## Contexto e motivação

O Velix API é single-tenant por padrão: o primeiro `POST /v1/auth/register`
cria o workspace + usuário admin, e o backend já fecha o registro depois
disso (`internal/api/auth/handler.go`, guarda via `Service.HasAnyWorkspace`).
Só que essa trava não existe no front-end: a tela de login (`/admin`,
`index.html`) sempre mostra a aba "Criar conta", mesmo depois de o admin
já existir — a tentativa só falha (403) ao submeter.

Além disso, hoje não existe nenhuma forma de o admin cadastrar outros
usuários do workspace pelo próprio painel. E o modelo de `Role` que já
existe (`admin/developer/viewer`) não é aplicado em nenhuma rota de
mensagens, instâncias ou contatos/grupos — qualquer usuário autenticado
via JWT tem acesso total, independente do role.

Este documento cobre duas mudanças relacionadas:

1. Esconder a aba "Criar conta" na tela de login quando já existe um
   admin no workspace.
2. Um subsistema de gestão de usuários no painel admin, com permissões
   granulares por usuário (visualizar/enviar/agendar mensagens, gerenciar
   instâncias, gerenciar contatos/grupos), aplicadas de fato nas rotas
   existentes.

Não está no escopo: multi-tenant / múltiplos workspaces (`REGISTRATION_ENABLED`
continua existindo e inalterado, mas não é o foco aqui), nem permissões por
instância específica (permissões são globais dentro do workspace, conforme
decidido com o usuário).

## Modelo de dados

### `role` (coluna existente em `users`)

Hoje: `VARCHAR(50) DEFAULT 'developer' CHECK (role IN ('admin','developer','viewer'))`.
`developer` e `viewer` nunca são referenciados fora de `entity.go` — não há
uso real hoje.

Passa a ser: `CHECK (role IN ('admin','member')) DEFAULT 'member'`.

- `admin`: acesso total a todas as rotas do workspace, incluindo gestão de
  usuários. Não é afetado por `permissions` (ignorado/bypassed).
- `member`: acesso controlado pelo array `permissions`.

### `permissions` (coluna nova em `users`)

`TEXT[] NOT NULL DEFAULT '{}'` — mesmo padrão já usado em `api_keys.scopes`
(`internal/domain/auth/entity.go`, campo `APIKey.Scopes`).

Valores válidos (constante em código, não CHECK constraint no banco — mesmo
padrão de scopes hoje):

- `messages:view` — ver conversas, histórico, busca de mensagens
- `messages:send` — enviar texto/mídia/reação/localização/enquete/contato,
  marcar como lido, apagar/revogar mensagem, upload de mídia. Também cobre
  **criar** um agendamento (ver seção "Agendamento" abaixo).
- `messages:schedule` — ver a fila de agendados e cancelar um agendamento
- `instances:manage` — CRUD de instância, conectar/desconectar, QR code,
  pairing, configurações, presença, perfil, sync Chatwoot
- `contacts:manage` — contatos e grupos

Gestão de usuários (criar/editar/remover) **não é uma permissão toggleável**
— fica restrita a `role = admin`, sempre.

### Migração `012_user_permissions`

`up.sql`:
```sql
ALTER TABLE users DROP CONSTRAINT users_role_check; -- nome auto-gerado do CHECK inline em 001_init.up.sql
ALTER TABLE users ADD COLUMN permissions TEXT[] NOT NULL DEFAULT '{}';
UPDATE users SET role = 'member' WHERE role IN ('developer', 'viewer');
UPDATE users SET permissions = ARRAY['messages:view','messages:send','messages:schedule','instances:manage','contacts:manage']
  WHERE role = 'member';
ALTER TABLE users ALTER COLUMN role SET DEFAULT 'member';
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin','member'));
```
`down.sql` reverte a constraint para o conjunto antigo e dropa a coluna
`permissions` (dados de permissão são perdidos no rollback — aceitável,
é uma migração de schema).

Backfill grandfathers todo usuário não-admin existente com as 5 permissões
ativas, preservando o comportamento atual (acesso total) para quem já usa
o sistema.

## Enforcement (middleware)

Novo `middleware.RequirePermission(perm string)` em
`internal/server/middleware/auth.go`, no mesmo espírito de `RequireRole`
(já usado em 2 rotas hoje) e `RequireScope` (já existe para API keys, mas
não é aplicado em rota nenhuma hoje):

```go
func RequirePermission(perm string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            claims := ClaimsFrom(r.Context())
            if claims == nil {
                apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
                return
            }
            // Requisições via API key (claims.Role == "") mantêm o
            // comportamento atual — não afetadas por este middleware.
            if claims.Role == "" || claims.Role == auth.RoleAdmin {
                next.ServeHTTP(w, r)
                return
            }
            if !slices.Contains(claims.Permissions, perm) {
                apipkg.WriteError(w, r, apipkg.ErrForbidden)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

`auth.Claims` ganha um campo novo `Permissions []string` (json `"perms,omitempty"`),
populado a partir de `user.Permissions` ao assinar o JWT em `Service.signToken`
(login e register).

Importante: **API keys não são afetadas** por esta mudança — seguem com o
comportamento atual (`RequireScope`, não aplicado em rota nenhuma hoje).
Este trabalho é só sobre sessões de usuário humano (JWT) no painel.

### Mapeamento rota → permissão

Aplicado em `internal/server/router.go`, como `.With(middleware.RequirePermission("..."))`
nos grupos de rota já existentes:

| Permissão | Rotas |
|---|---|
| `messages:view` | `GET /messages`, `GET /messages/search` |
| `messages:send` | `POST /messages/{text,media,reaction,read,batch,location,poll,contact}`, `DELETE /messages/{msgID}`, `POST /media` |
| `messages:schedule` | `GET /messages/scheduled`, `DELETE /messages/{msgID}/schedule` |
| `instances:manage` | toda a árvore `/instances/**` (inclui settings, presence, profile, chatwoot sync) |
| `contacts:manage` | `/contacts/**`, `/groups/**` |
| admin-only, sem toggle | `/audit-logs`, `/admin/stats`, `/metrics`, `/users/**` (novo) |

### Agendamento — decisão confirmada

Como o agendamento é só o campo `scheduled_at` no mesmo endpoint de envio
(não uma rota separada), **criar** uma mensagem agendada exige apenas
`messages:send`. `messages:schedule` controla exclusivamente visualizar a
fila (`GET /scheduled`) e cancelar (`DELETE /{msgID}/schedule`).

## API de gestão de usuários

Novo módulo `internal/api/user`, montado em `/v1/users`, atrás de
`authMiddleware` + `middleware.RequireRole(auth.RoleAdmin)`:

- `POST /v1/users` — body `{email, password, role, permissions[]}`.
  - Valida email/senha com as mesmas regras de `Register` (reaproveitar
    `validatePassword` do `domain/auth`).
  - Se `role == "admin"`, ignora `permissions` recebido e salva `[]`.
  - Se `role == "member"`, salva só os valores dentre o conjunto válido
    (ignora desconhecidos, não dá erro).
- `GET /v1/users` — lista usuários do workspace (id, email, role,
  permissions, last_login_at, created_at). Nunca retorna `password_hash`.
- `PATCH /v1/users/{id}` — atualiza `role`/`permissions` e,
  opcionalmente, `password` (reset). Não permite alterar o próprio
  usuário autenticado para tirar o papel admin dele mesmo (evita
  self-lockout: um admin não pode se rebaixar).
- `DELETE /v1/users/{id}` — remove usuário. Bloqueia se `id` for o
  próprio usuário autenticado (evita remover a si mesmo e ficar sem
  admin).

Novo `internal/domain/auth/*` ganha os métodos de `Service`
(`CreateUser`, `ListUsers`, `UpdateUser`, `DeleteUser`) e o `Repository`
correspondente (`internal/infra/repo/auth_repo.go`).

## Painel admin (frontend)

### `index.html` (login)

- Novo endpoint público `GET /v1/auth/registration-status` →
  `{"open": bool}` (reflete `HasAnyWorkspace` + `REGISTRATION_ENABLED`,
  mesma regra do handler de registro).
- No `init()` do Alpine, busca esse status; se `open === false`, esconde
  a aba/botão "Criar conta" (fica só "Entrar").

### Nova página `users.html`

Segue o padrão visual das páginas existentes (`dashboard.html`,
`instances.html`) — mesmo layout de sidebar/topbar, `alpine.min.js` +
`main.css`, sem novas dependências.

- Link "Usuários" na sidebar, visível apenas quando o usuário logado é
  admin (checagem client-side pelo `role` retornado em `/v1/auth/me`,
  reforçada pelo backend via `RequireRole`).
- Tabela listando usuários do workspace: email, role, badges de
  permissão, último login.
- Botão "+ Novo usuário" abre um formulário modal: email, senha, seletor
  de role (Admin / Membro), e — só quando "Membro" — 5 checkboxes (uma
  por permissão, com label amigável).
- Ações por linha: editar permissões, resetar senha, remover (com
  confirmação).

## Testes

- `internal/domain/auth/service_test.go`: casos novos para
  `CreateUser`/`UpdateUser`/`DeleteUser` (self-lockout, permissões
  inválidas ignoradas, admin ignora `permissions`).
- `internal/server/middleware/*_test.go`: novo `RequirePermission` —
  admin sempre passa, member com/sem a permissão, API key não afetada.
- Teste de integração leve do fluxo: registrar workspace → criar membro
  com só `messages:view` → confirmar que `POST /messages/text` retorna
  403 e `GET /messages` retorna 200.

## Fora de escopo (YAGNI)

- Permissões por instância específica (decidido: permissões são globais
  no workspace).
- Tornar "gerenciar usuários" uma permissão toggleável (decidido:
  sempre admin-only).
- Qualquer mudança em `REGISTRATION_ENABLED` / multi-tenant.
- Mudanças no modelo de scopes de API key (`APIKey.Scopes`) — sistema
  separado, não tocado aqui.
