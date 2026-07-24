import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Braces, Plus } from "lucide-react";
import { Link } from "react-router-dom";
import { createParserProfile, listParserProfiles } from "../api";
import { formatTimestamp } from "../lib/format";

export function Parsers() {
  const client = useQueryClient();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const profiles = useQuery({
    queryKey: ["parser-profiles"],
    queryFn: listParserProfiles,
  });
  const create = useMutation({
    mutationFn: createParserProfile,
    onSuccess: () => {
      setName("");
      setDescription("");
      void client.invalidateQueries({ queryKey: ["parser-profiles"] });
    },
  });

  return (
    <section className="page studio-page">
      <div className="eyebrow">Extraction Studio · v0.4</div>
      <div className="page-heading-row">
        <div>
          <h1>Teach LogLens what each record means.</h1>
          <p className="lede">
            Versioned profiles turn arbitrary text into typed, searchable events
            without changing the raw source.
          </p>
        </div>
      </div>

      <div className="studio-grid">
        <section className="panel studio-list">
          <div className="panel-heading">
            <div>
              <h2>Parser profiles</h2>
              <p>Reusable extraction contracts with immutable versions</p>
            </div>
          </div>
          {profiles.isError ? (
            <div className="error-state">{profiles.error.message}</div>
          ) : profiles.data?.length ? (
            <div className="profile-list">
              {profiles.data.map((profile) => (
                <Link
                  key={profile.id}
                  to={`/parsers/${profile.id}`}
                  className="profile-card"
                >
                  <span className="file-icon">
                    <Braces size={17} />
                  </span>
                  <span>
                    <strong>{profile.name}</strong>
                    <small>
                      {profile.description || "No description"} · updated{" "}
                      {formatTimestamp(profile.updatedAt)}
                    </small>
                  </span>
                  <ArrowRight size={16} />
                </Link>
              ))}
            </div>
          ) : (
            <div className="empty-state">
              No parser profile yet. Create one for a log format you understand.
            </div>
          )}
        </section>

        <form
          className="panel studio-create"
          onSubmit={(event) => {
            event.preventDefault();
            create.mutate({ name, description });
          }}
        >
          <div className="panel-heading">
            <div>
              <h2>New profile</h2>
              <p>Versions are added after the profile is created</p>
            </div>
          </div>
          <label className="form-field">
            <span>Name</span>
            <input
              required
              maxLength={120}
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="Custom Combat Logs"
            />
          </label>
          <label className="form-field">
            <span>Description</span>
            <textarea
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              placeholder="Events emitted by the dedicated game server"
            />
          </label>
          {create.isError ? (
            <div className="error-state compact">{create.error.message}</div>
          ) : null}
          <button
            className="button button-primary"
            disabled={!name.trim() || create.isPending}
          >
            <Plus size={16} /> Create profile
          </button>
        </form>
      </div>
    </section>
  );
}
