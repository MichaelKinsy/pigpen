import { Type } from "typebox";
console.log(JSON.stringify(Type.Object({ handoff: Type.Optional(Type.String({ description: "What the fresh window needs to continue, as readable prose", maxLength: 20_000 })) })));
console.log(JSON.stringify(Type.Object({})));
