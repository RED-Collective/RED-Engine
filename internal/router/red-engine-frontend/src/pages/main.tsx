// This is the main page for the node

export default function MainPage() {
  return (
    <div className="flex-1 flex bg-cover bg-center bg-no-repeat relative">
      {/* Background div */}
      <div className="bg-[url('images/school_of_athens.jpg')] bg-position-[center_60%] sepia-80 opacity-75  absolute inset-0 bg-cover bg-no-repeat z-0"></div>
      {/* Opacity gradient to bottom */}
      <div className="absolute inset-0 bg-linear-to-b from-transparent to-white" />

      <div className="flex flex-1 flex-col justify-items-center align-middle z-10 p-auto">
        <h2 className="flex text-center mt-24 mx-auto text-3xl font-sans p-16">
          RED Engine Protocol
        </h2>

        <div>
          <h1 className="mx-auto justify-center font-black text-neutral-900 text-7xl text-center">
            Decentralized, Stateless, Verifiable
          </h1>
          <h1 className="mx-auto justify-center text-center text-7xl font-black text-brand-500">
            Knowledge
          </h1>
        </div>

        <div className="mt-8 h-2 w-3xl bg-brand-600 mx-auto" />
        <p className="italic text-2xl font-sans p-8 mt-24 mx-auto mb-auto text-brand-950">
          "Reclaiming the intellectual commons through secure decentralization."
        </p>
      </div>
    </div>
  );
}
