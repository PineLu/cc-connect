#!/usr/bin/env python3
"""Project-scoped ACP backend switcher for cc-connect (Python >= 3.11)."""
from __future__ import annotations
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import selectors
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import tomllib

PROJECT = "tujia-feishu-hermes"
LABEL = "com.cc-connect.service"
FIELDS = ("cmd", "args", "extension", "display_name", "mode", "model_command", "compress_command")
OTHER = {
    "codebuddy": {"cmd": "/opt/homebrew/bin/codebuddy", "args": ["--acp"],
                  "extension": "generic", "display_name": "CodeBuddy (ACP)"},
    "qodercn": {"cmd": str(Path.home() / ".local/bin/qoderclicn"),
                "args": ["--acp"], "extension": "generic", "display_name": "QoderCN (ACP)"},
    # Cline CLI's --auto-approve default differs across versions; use an
    # explicit false value so file edits/terminal calls require approval.
    "cline": {"cmd": "/opt/homebrew/bin/cline",
              "args": ["--acp", "--auto-approve", "false"],
              "extension": "generic", "display_name": "Cline (ACP)"},
    # All cc-connect Codex ACP sessions use a shared, independently
    # authenticated CODEX_HOME; never inherit the desktop ~/.codex state.
    "codex": {"cmd": str(Path(__file__).resolve().parent / "cc-codex"),
              "args": ["acp"], "extension": "generic", "display_name": "Codex (ACP)"},
}
BACKENDS = ("hermes", *OTHER)
LEGACY_CODEX = {"cmd": "/opt/homebrew/bin/codex-acp", "args": [],
                "extension": "generic", "display_name": "Codex (ACP)"}

def snapshot_name(backend, profile):
    # Legacy ~/.codex thread IDs cannot be loaded by isolated CODEX_HOME.
    # Keep the old snapshot as codex.json, and use a separate name henceforth.
    return "codex-isolated" if backend == "codex" and profile == OTHER["codex"] else backend

def codex_isolation_upgrade(profiles, target, strict=True):
    if target != "codex":
        return False
    existing = profiles["codex"]
    if existing == OTHER["codex"]:
        return False
    if existing != LEGACY_CODEX:
        if strict:
            raise Refuse("custom Codex ACP profile; cannot automatically migrate to isolated CODEX_HOME")
        return False
    return True

def require_independent_codex_login():
    root = Path.home() / ".cc-connect" / ".codex-home"
    if root.is_symlink() or not root.is_dir():
        raise Refuse("private Codex Home is missing or unsafe")
    auth = root / "auth.json"
    if auth.is_symlink():
        raise Refuse("independent Codex auth must not be a symlink to desktop credentials")
    proc = subprocess.run([str(Path(__file__).resolve().parent / "cc-codex"), "login-status"],
                          capture_output=True, text=True, timeout=20)
    if proc.returncode != 0:
        raise Refuse("cc-connect Codex is not independently logged in; run tools/cc-codex login --device-auth")


class Refuse(RuntimeError):
    pass

def atomic(path, content, mode=0o600):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(prefix=".cc-acp-", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as f:
            f.write(content)
            f.flush()
            os.fsync(f.fileno())
        os.chmod(tmp, mode)
        os.replace(tmp, path)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)

def regular(path):
    if path.is_symlink() or (path.exists() and not path.is_file()):
        raise Refuse(f"unsafe path: {path}")

def get_config(path):
    raw = path.read_bytes()
    obj = tomllib.loads(raw.decode())
    matches = [p for p in obj.get("projects", []) if p.get("name") == PROJECT]
    if len(matches) != 1 or matches[0].get("agent", {}).get("type") != "acp":
        raise Refuse("target project missing, duplicated, or not type=acp")
    return raw, obj, matches[0]

def fields(options):
    return {k: options[k] for k in FIELDS if k in options}

def private_root(cfg):
    raw = cfg.get("data_dir") or str(Path.home() / ".cc-connect")
    data = Path(os.path.expandvars(os.path.expanduser(raw))).resolve()
    return data / "acp-switch" / PROJECT, data

def session_path(data, options):
    # cc-connect may override the configured work_dir from project-state
    # before choosing the session filename. V1 rejects that special case
    # rather than risking cross-vendor session ID leakage.
    project_state = data / "projects" / (PROJECT + ".state.json")
    regular(project_state)
    if project_state.exists():
        runtime_state = json.loads(project_state.read_text())
        if runtime_state.get("work_dir_override"):
            raise Refuse("runtime work_dir_override is active; cannot safely switch sessions")
    work = options.get("work_dir")
    if not isinstance(work, str):
        raise Refuse("ACP work_dir must be a string")
    # Match config.Load env substitution and expandHomeInConfig.
    # Non-HOME variables may differ in shell vs launchd; fail closed.
    placeholders = re.findall(r"\x24\x7b([A-Za-z_][A-Za-z0-9_]*)\x7d", work)
    if any(key != "HOME" for key in placeholders):
        raise Refuse("non-HOME env expansion in work_dir needs manual session-path verification")
    work = work.replace(chr(36) + "{HOME}", str(Path.home()))
    if work == "~":
        work = str(Path.home())
    elif work.startswith("~/"):
        work = str(Path.home() / work[2:])
    if not os.path.isabs(work):
        raise Refuse("absolute ACP work_dir required to isolate sessions")
    digest = hashlib.sha256(os.fsencode(os.path.abspath(work))).hexdigest()[:8]
    stem = PROJECT + "_" + digest
    for item in (data / (stem + ".json"), data / (stem + ".sessions.json")):
        if item.exists():
            return item
    return data / "sessions" / (stem + ".json")

def ensure_single_workspace(config, data):
    project=next(p for p in config["projects"] if p.get("name")==PROJECT)
    if project.get("mode")=="multi-workspace":
        raise Refuse("multi-workspace ACP projects need per-workspace isolation; V1 refuses")
    for directory in (data, data/"sessions"):
        if directory.exists() and any(directory.glob(PROJECT+"_ws_*.json")):
            raise Refuse("workspace-specific ACP session files exist; V1 refuses to switch")

def load_state(root):
    f = root / "state.json"
    regular(f)
    if not f.exists():
        return None
    state = json.loads(f.read_text())
    if state.get("active") not in BACKENDS:
        raise Refuse("bad managed state")
    if not isinstance(state.get("profiles"), dict) or state["active"] not in state["profiles"]:
        raise Refuse("active ACP backend missing from managed profiles")
    return state

def profile_and_active(options, state):
    if state:
        active = state["active"]
        if fields(options) != state["profiles"][active]:
            raise Refuse("ACP configuration modified outside switcher; refusing to overwrite")
        # Old V1 installations persist only three backend profiles. Add newly
        # supported presets in memory without rewriting the state on status,
        # list, check or dry-run, and keep existing user profiles untouched.
        profiles = dict(state["profiles"])
        for name, preset in OTHER.items():
            profiles.setdefault(name, dict(preset))
        return {**state, "profiles": profiles}, active
    if options.get("cmd") != "hermes" or options.get("args") != ["-p", "tujia", "acp"] or options.get("extension") != "hermes":
        raise Refuse("first use requires unchanged Hermes ACP configuration")
    return {"active": "hermes", "previous": None,
            "profiles": {"hermes": fields(options), **OTHER}}, "hermes"

def encode_toml_value(value):
    if isinstance(value, str):
        return json.dumps(value, ensure_ascii=False)
    if isinstance(value, list) and all(isinstance(x, str) for x in value):
        return "[" + ", ".join(json.dumps(x, ensure_ascii=False) for x in value) + "]"
    raise Refuse("unsupported ACP option type")

def patch_config(raw, newfields):
    lines = raw.decode().splitlines(keepends=True)
    starts = [i for i, l in enumerate(lines) if l.strip() == "[[projects]]"]
    target = []
    for n, a in enumerate(starts):
        b = starts[n+1] if n+1 < len(starts) else len(lines)
        if any(l.strip() == 'name = "' + PROJECT + '"' for l in lines[a:b]):
            target.append((a,b))
    if len(target) != 1:
        raise Refuse("non-unique target TOML section")
    a,b = target[0]
    heads = [i for i in range(a,b) if lines[i].strip() == "[projects.agent.options]"]
    if len(heads) != 1:
        raise Refuse("non-unique project options TOML section")
    first = heads[0]+1
    last = next((i for i in range(first,b) if re.match(r"^\s*\[+[a-zA-Z]",lines[i])),b)
    oldregion = lines[first:last]
    keep = [l for l in oldregion if not (re.match(r"^\s*(\w+)\s*=",l) and re.match(r"^\s*(\w+)\s*=",l).group(1) in FIELDS)]
    prefix = [k + " = " + encode_toml_value(v) + "\n" for k,v in newfields.items()]
    newraw = "".join(lines[:first]+prefix+keep+lines[last:]).encode()
    before = tomllib.loads(raw.decode())["projects"]
    after = tomllib.loads(newraw.decode())["projects"]
    if len(before) != len(after):
        raise Refuse("projects count changed")
    for x,y in zip(before, after):
        if x["name"] != y["name"]:
            raise Refuse("projects order changed")
        if x["name"] != PROJECT and x != y:
            raise Refuse("unrelated project modified")
        if x["name"] == PROJECT:
            ox={k:v for k,v in x["agent"]["options"].items() if k not in FIELDS}
            oy={k:v for k,v in y["agent"]["options"].items() if k not in FIELDS}
            if ox != oy or y["agent"]["type"] != "acp" or {k:v for k,v in x.items() if k!="agent"} != {k:v for k,v in y.items() if k!="agent"}:
                raise Refuse("unrelated target settings modified")
    return newraw

def binary(profile):
    cmd=shutil.which(profile["cmd"])
    if not cmd or not os.access(cmd,os.X_OK):
        raise Refuse("ACP executable unavailable: "+profile["cmd"])
    return cmd

def probe(profile, timeout=24):
    """ACP initialize only; never session/new, session/prompt, or tool calls."""
    argv=[binary(profile),*profile.get("args",[])]
    req={"jsonrpc":"2.0","id":1,"method":"initialize",
         "params":{"protocolVersion":1,
                   "clientCapabilities":{"fs":{"readTextFile":False,"writeTextFile":False},"terminal":False},
                   "clientInfo":{"name":"cc-acp-switch","version":"1.0"}}}
    proc=subprocess.Popen(argv,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,start_new_session=True)
    try:
        proc.stdin.write((json.dumps(req)+"\n").encode())
        proc.stdin.flush()
        sel=selectors.DefaultSelector()
        sel.register(proc.stdout, selectors.EVENT_READ)
        buf=b""
        deadline=time.monotonic()+timeout
        while time.monotonic()<deadline:
            ev=sel.select(min(0.25,max(0,deadline-time.monotonic())))
            if not ev:
                if proc.poll() is not None: break
                continue
            chunk=os.read(proc.stdout.fileno(),4096)
            if not chunk: break
            buf+=chunk
            if len(buf)>65536: raise Refuse("ACP probe exceeded output limit")
            while b"\n" in buf:
                line,buf=buf.split(b"\n",1)
                try: msg=json.loads(line)
                except (ValueError,UnicodeDecodeError): continue
                if msg.get("id")==1:
                    if isinstance(msg.get("result"),dict) and msg["result"].get("protocolVersion"):
                        return
                    raise Refuse("ACP initialize rejected by target")
        raise Refuse("ACP initialize failed/timed out; no config changed")
    finally:
        try: os.killpg(proc.pid,signal.SIGTERM)
        except ProcessLookupError: pass
        try: proc.wait(timeout=2)
        except subprocess.TimeoutExpired:
            try: os.killpg(proc.pid,signal.SIGKILL)
            except ProcessLookupError: pass
            proc.wait(timeout=2)

def service_state():
    out=subprocess.run(["launchctl","print",f"gui/{os.getuid()}/{LABEL}"],
                       capture_output=True,text=True,timeout=12)
    if out.returncode: return "absent",None
    m=re.search(r"^\s*state = (\w+)",out.stdout,re.M)
    p=re.search(r"^\s*pid = (\d+)",out.stdout,re.M)
    return (m.group(1) if m else "unknown",int(p.group(1)) if p else None)

def service(plist, action):
    cmd=["launchctl","bootout" if action=="stop" else "bootstrap",
         f"gui/{os.getuid()}",str(plist)]
    out=subprocess.run(cmd,capture_output=True,text=True,timeout=40)
    if out.returncode:
        raise Refuse(f"launchd {action} failed, exit {out.returncode}")

def await_state(up, timeout=35):
    deadline=time.monotonic()+timeout
    while time.monotonic()<deadline:
        state,pid=service_state()
        if up and state=="running" and pid:
            time.sleep(2)
            check,pid2=service_state()
            if check=="running" and pid2==pid: return
        if not up and state=="absent": return
        time.sleep(0.5)
    raise Refuse("launchd not healthy" if up else "service did not exit")

def pending_path(root):
    return root / "pending.json"

def ensure_no_pending(root):
    pending=pending_path(root)
    regular(pending)
    if pending.exists():
        raise Refuse("incomplete ACP transaction; run cc-acp recover --yes before switching")

def recover(configfile, plist, yes=False):
    raw,cfg,project=get_config(configfile)
    root,data=private_root(cfg)
    journal=pending_path(root)
    regular(journal)
    if not journal.exists():
        return "no pending ACP transaction"
    if not yes:
        raise Refuse("recovery restarts all cc-connect projects; pass --yes")
    with (root/".lock").open("a+b") as lock:
        fcntl.flock(lock,fcntl.LOCK_EX | fcntl.LOCK_NB)
        info=json.loads(journal.read_text())
        ident=info.get("transaction","")
        if not re.fullmatch(r"[0-9T-]+",ident):
            raise Refuse("invalid recovery transaction id")
        txn=root/"transactions"/ident
        original=txn/"config.toml"
        regular(original)
        original_bytes=original.read_bytes()
        original_config=tomllib.loads(original_bytes.decode())
        # Ensure the backup belongs to the same target project.
        if not any(p.get("name")==PROJECT for p in original_config.get("projects",[])):
            raise Refuse("recovery config belongs to another project")
        session=session_path(data,project["agent"]["options"])
        regular(session)
        session_backup=txn/"session.json"
        state_backup=txn/"state.json"
        saved_backend=info.get("from")
        if saved_backend not in BACKENDS:
            raise Refuse("invalid source backend in transaction")
        snapshot_key=info.get("previous_snapshot", saved_backend)
        if snapshot_key not in (*BACKENDS, "codex-isolated"):
            raise Refuse("invalid recovery session snapshot key")
        previous_snapshot=root/"sessions"/(snapshot_key+".json")
        previous_backup=txn/"previous-snapshot.json"
        for x in (session_backup,state_backup,previous_snapshot,previous_backup):
            regular(x)
        if info.get("had_session") and not session_backup.is_file():
            raise Refuse("missing backed up session; manual recovery required")
        if info.get("had_state") and not state_backup.is_file():
            raise Refuse("missing backed up switch state; manual recovery required")
        if info.get("had_previous_snapshot") and not previous_backup.is_file():
            raise Refuse("missing previous backend snapshot; manual recovery required")
        if service_state()[0]!="absent":
            service(plist,"stop")
            await_state(False)
        atomic(configfile,original_bytes,configfile.stat().st_mode & 0o777)
        if info.get("had_session"):
            atomic(session,session_backup.read_bytes(),0o644)
        else:
            session.unlink(missing_ok=True)
        if info.get("had_state"):
            atomic(root/"state.json",state_backup.read_bytes())
        else:
            (root/"state.json").unlink(missing_ok=True)
        if info.get("had_previous_snapshot"):
            atomic(previous_snapshot,previous_backup.read_bytes())
        else:
            previous_snapshot.unlink(missing_ok=True)
        service(plist,"start")
        await_state(True)
        journal.unlink()
        return "recovered previous ACP config, session, state and service"

def use(configfile, plist, target, dry_run=False, yes=False, probe_target=True):
    raw,cfg,project=get_config(configfile)
    opts=project["agent"]["options"]
    root,data=private_root(cfg)
    ensure_no_pending(root)
    ensure_single_workspace(cfg,data)
    state0=load_state(root)
    definition,old=profile_and_active(opts,state0)
    upgrade=codex_isolation_upgrade(definition["profiles"], target)
    if old==target and not upgrade:
        return f"already using {target}; no restart"
    profile=OTHER["codex"] if upgrade else definition["profiles"][target]
    session=session_path(data,opts)
    regular(configfile); regular(session); regular(plist)
    binary(profile)
    next_config=patch_config(raw,profile)
    if dry_run:
        detail=" (isolate Codex Home)" if upgrade else ""
        return f"PLAN {old} -> {target}{detail}; preserve {session.name} separately; restart whole service; no changes"
    if not yes:
        raise Refuse("will restart all cc-connect projects; use --yes only when no active tasks")
    if target=="codex":
        require_independent_codex_login()
    if probe_target:
        probe(profile)
    # Private store and advisory lock: only one switcher process at a time.
    if root.is_symlink() or (root.exists() and not root.is_dir()):
        raise Refuse("unsafe managed state directory")
    root.mkdir(parents=True,exist_ok=True,mode=0o700)
    os.chmod(root,0o700)
    with (root/".lock").open("a+b") as lock:
        try: fcntl.flock(lock,fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError: raise Refuse("another ACP switch is running")
        ensure_no_pending(root)
        if raw!=configfile.read_bytes() or state0!=load_state(root):
            raise Refuse("configuration/state changed while probing")
        if not plist.is_file():
            raise Refuse("launchd plist missing; refusing to stop service")
        if service_state()[0]!="running": raise Refuse("cc-connect is not running")
        # Stop before touching any session file: Engine.Stop persists the final state.
        service(plist,"stop")
        try:
            await_state(False)
        except Exception:
            if service_state()[0]=="absent": service(plist,"start")
            raise
        before_session=None
        previous_data=None
        session_changed=False
        previous_changed=False
        txn=root/"transactions"/(time.strftime("%Y%m%dT%H%M%S")+"-"+str(os.getpid())+"-"+str(time.time_ns()))
        old_snapshot_key=snapshot_name(old, definition["profiles"][old])
        next_snapshot_key=snapshot_name(target, profile)
        prev_snapshot=root/"sessions"/(old_snapshot_key+".json")
        target_snapshot=root/"sessions"/(next_snapshot_key+".json")
        if prev_snapshot==target_snapshot:
            raise Refuse("cannot isolate ACP session snapshot when paths are equal")
        try:
            # Re-evaluate after shutdown in case runtime workdir state changed.
            if session_path(data,opts) != session:
                raise Refuse("session path changed during switch")
            ensure_single_workspace(get_config(configfile)[1],data)
            regular(prev_snapshot); regular(target_snapshot)
            regular(root/"state.json")
            txn.mkdir(parents=True,mode=0o700)
            atomic(txn/"config.toml",raw)
            prior_state=root/"state.json"
            if prior_state.exists():
                atomic(txn/"state.json",prior_state.read_bytes())
            if session.exists():
                before_session=session.read_bytes()
                json.loads(before_session)
                atomic(txn/"session.json",before_session)
            if prev_snapshot.exists():
                previous_data=prev_snapshot.read_bytes()
                json.loads(previous_data)
                atomic(txn/"previous-snapshot.json",previous_data)
            incoming=target_snapshot.read_bytes() if target_snapshot.exists() else None
            if incoming is not None:
                json.loads(incoming)
            # Durable journal + all backups must exist before touching ANY
            # vendor snapshot, live session file, or config.
            journal={"transaction":txn.name,"from":old,
                     "previous_snapshot":old_snapshot_key,
                     "had_session":before_session is not None,
                     "had_state":prior_state.exists(),
                     "had_previous_snapshot":previous_data is not None}
            atomic(pending_path(root),json.dumps(journal).encode())
            previous_changed=True
            if before_session is not None:
                atomic(prev_snapshot,before_session)
            else:
                prev_snapshot.unlink(missing_ok=True)
            if incoming is not None:
                session_changed=True
                atomic(session,incoming,0o644)
            else:
                session_changed=True
                session.unlink(missing_ok=True)
            atomic(configfile,next_config,configfile.stat().st_mode & 0o777)
            service(plist,"start")
            await_state(True)
            updated=dict(definition)
            updated["profiles"]=dict(definition["profiles"])
            if upgrade:
                updated["profiles"]["codex"]=dict(profile)
            updated["active"]=target
            # Re-profiling an active Codex is a migration, not a backend
            # switch; keep rollback pointing at the previous *other* agent.
            updated["previous"]=definition.get("previous") if old==target else old
            atomic(root/"state.json",json.dumps(updated,ensure_ascii=False,indent=2).encode())
            pending_path(root).unlink()
        except Exception as err:
            try:
                if service_state()[0]!="absent":
                    service(plist,"stop");await_state(False)
                atomic(configfile,raw,configfile.stat().st_mode & 0o777)
                if session_changed:
                    if before_session is None:
                        session.unlink(missing_ok=True)
                    else:
                        atomic(session,before_session,0o644)
                if state0 is None:
                    (root/"state.json").unlink(missing_ok=True)
                else:
                    atomic(root/"state.json",(txn/"state.json").read_bytes())
                if previous_changed:
                    if previous_data is None:
                        prev_snapshot.unlink(missing_ok=True)
                    else:
                        atomic(prev_snapshot,previous_data)
                service(plist,"start");await_state(True)
                pending_path(root).unlink(missing_ok=True)
            except Exception as recovery:
                raise Refuse(f"FAILED; manual recovery required from {txn}: {recovery}") from err
            raise Refuse(f"switch failed; original service restored: {err}") from err
        return f"switched {old} -> {target}; service running; sessions isolated; backup {txn}"

def main():
    ap=argparse.ArgumentParser(description="Project-scoped ACP backend switcher")
    ap.add_argument("--config",type=Path,default=Path.home()/".cc-connect/config.toml")
    ap.add_argument("--plist",type=Path,default=Path.home()/"Library/LaunchAgents/com.cc-connect.service.plist")
    sub=ap.add_subparsers(dest="action",required=True)
    sub.add_parser("status")
    sub.add_parser("list")
    check=sub.add_parser("check")
    check.add_argument("backend",choices=BACKENDS)
    choice=sub.add_parser("use")
    choice.add_argument("backend",choices=BACKENDS)
    back=sub.add_parser("rollback")
    fix=sub.add_parser("recover")
    fix.add_argument("--yes",action="store_true",help="acknowledge service-wide restart")
    for node in (choice,back):
        node.add_argument("--dry-run",action="store_true")
        node.add_argument("--yes",action="store_true",help="acknowledge service-wide restart")
    args=ap.parse_args()
    try:
        raw,cfg,project=get_config(args.config)
        root,_=private_root(cfg)
        if args.action=="recover":
            print(recover(args.config,args.plist,yes=args.yes))
            return 0
        ensure_no_pending(root)
        state,active=profile_and_active(project["agent"]["options"],
                                        load_state(root))
        if args.action=="status":
            live,pid=service_state()
            legacy=" (legacy shared Codex Home; migration pending)" if (
                active=="codex" and codex_isolation_upgrade(state["profiles"],"codex",strict=False)) else ""
            print(f"{PROJECT}: {active}{legacy}; service={live}, pid={pid or '-'}")
        elif args.action=="list":
            for name,profile in state["profiles"].items():
                status='ACTIVE' if name==active else 'available' if shutil.which(profile['cmd']) else 'MISSING'
                if name=="codex" and codex_isolation_upgrade(state["profiles"],"codex",strict=False):
                    status += " (legacy shared Home; isolated upgrade available)"
                print(f"{name}: {status}")
        elif args.action=="check":
            upgrade=codex_isolation_upgrade(state["profiles"],args.backend,strict=False)
            probe(OTHER["codex"] if upgrade else state["profiles"][args.backend])
            print(f"{args.backend}: initialize PASS (no chat or session created)")
        else:
            target=args.backend if args.action=="use" else state.get("previous")
            if not target: raise Refuse("no previous backend for rollback")
            print(use(args.config,args.plist,target,
                      dry_run=args.dry_run,yes=args.yes))
        return 0
    except (Refuse,OSError,ValueError,subprocess.TimeoutExpired) as error:
        print(f"cc-acp: {error}",file=sys.stderr)
        return 1

if __name__=="__main__":
    sys.exit(main())
